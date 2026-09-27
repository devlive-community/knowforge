// Package jobqueue provides a small database-backed background task queue.
package jobqueue

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"knowforge/server/internal/models"

	"gorm.io/gorm"
)

const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusRetrying  = "retrying"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"

	defaultMaxAttempts = 5
	// staleJobAge 执行中的任务超过这段时间没有心跳，视为执行它的进程已退出（多实例时其他实例据此接手）
	staleJobAge        = 5 * time.Minute
	defaultHeartbeat   = time.Minute
	recoverEvery       = time.Minute
	completedRetention = 30 * 24 * time.Hour
)

type Handler func(context.Context, json.RawMessage) error
type ResultHandler func(context.Context, json.RawMessage) (any, error)

// Queue stores encrypted payloads in the application database and executes one
// claimed task at a time. Conditional status updates make claiming safe when
// multiple application processes share the same database; running tasks refresh
// locked_at as a heartbeat so that only tasks of exited processes are recovered.
type Queue struct {
	db             *gorm.DB
	aead           cipher.AEAD
	handlers       map[string]Handler
	resultHandlers map[string]ResultHandler
	mu             sync.RWMutex
	now            func() time.Time
	heartbeatEvery time.Duration // 执行中的任务刷新 locked_at 的间隔
}

func New(db *gorm.DB, secret string) (*Queue, error) {
	if db == nil {
		return nil, errors.New("job queue database is required")
	}
	if strings.TrimSpace(secret) == "" {
		return nil, errors.New("job queue encryption secret is required")
	}
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("create job payload cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create job payload AEAD: %w", err)
	}
	return &Queue{db: db, aead: aead, handlers: map[string]Handler{}, resultHandlers: map[string]ResultHandler{}, now: time.Now}, nil
}

func (q *Queue) RegisterResult(jobType string, handler ResultHandler) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.resultHandlers[jobType] = handler
}

func (q *Queue) Register(jobType string, handler Handler) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.handlers[jobType] = handler
}

// Types 已注册的任务类型（排序后），供管理端筛选与展示。
func (q *Queue) Types() []string {
	q.mu.RLock()
	defer q.mu.RUnlock()
	out := make([]string, 0, len(q.handlers)+len(q.resultHandlers))
	for t := range q.handlers {
		out = append(out, t)
	}
	for t := range q.resultHandlers {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func (q *Queue) Enqueue(ctx context.Context, jobType string, payload any, maxAttempts int) (*models.BackgroundJob, error) {
	return q.EnqueueOwned(ctx, 0, jobType, payload, maxAttempts)
}

func (q *Queue) EnqueueOwned(ctx context.Context, ownerID uint, jobType string, payload any, maxAttempts int) (*models.BackgroundJob, error) {
	job, err := q.newJob(ownerID, jobType, payload, maxAttempts)
	if err != nil {
		return nil, err
	}
	if err := q.db.WithContext(ctx).Create(job).Error; err != nil {
		return nil, fmt.Errorf("create background job: %w", err)
	}
	return job, nil
}

// EnqueueIfDue creates a system task only when the same type is neither active
// nor completed within cooldown. It lets periodic maintenance survive restarts
// without filling the queue with duplicate tasks.
func (q *Queue) EnqueueIfDue(ctx context.Context, jobType string, payload any, maxAttempts int, cooldown time.Duration) (*models.BackgroundJob, bool, error) {
	job, err := q.newJob(0, jobType, payload, maxAttempts)
	if err != nil {
		return nil, false, err
	}
	created := false
	err = q.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var active int64
		if err := tx.Model(&models.BackgroundJob{}).
			Where("type = ? AND status IN ?", job.Type, []string{StatusPending, StatusRunning, StatusRetrying}).
			Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return nil
		}
		if cooldown > 0 {
			var recent int64
			if err := tx.Model(&models.BackgroundJob{}).
				Where("type = ? AND status = ? AND finished_at >= ?", job.Type, StatusSucceeded, job.CreatedAt.Add(-cooldown)).
				Count(&recent).Error; err != nil {
				return err
			}
			if recent > 0 {
				return nil
			}
		}
		if err := tx.Create(job).Error; err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("create due background job: %w", err)
	}
	if !created {
		return nil, false, nil
	}
	return job, true, nil
}

func (q *Queue) newJob(ownerID uint, jobType string, payload any, maxAttempts int) (*models.BackgroundJob, error) {
	jobType = strings.TrimSpace(jobType)
	if jobType == "" {
		return nil, errors.New("job type is required")
	}
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal job payload: %w", err)
	}
	sealed, err := q.encrypt(jobType, raw)
	if err != nil {
		return nil, err
	}
	now := q.now()
	job := &models.BackgroundJob{
		OwnerID: ownerID, Type: jobType, Payload: sealed, Status: StatusPending,
		MaxAttempts: maxAttempts, AvailableAt: now, CreatedAt: now, UpdatedAt: now,
	}
	return job, nil
}

// RunOnce claims and runs the next available task. ran is false when no task
// is currently available. A handler error is persisted for retry and returned
// only for observability; callers should continue processing later tasks.
func (q *Queue) RunOnce(ctx context.Context) (ran bool, runErr error) {
	now := q.now()
	var candidate models.BackgroundJob
	err := q.db.WithContext(ctx).
		Where("status IN ? AND available_at <= ?", []string{StatusPending, StatusRetrying}, now).
		Order("available_at ASC, id ASC").First(&candidate).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("find background job: %w", err)
	}

	claim := q.db.WithContext(ctx).Model(&models.BackgroundJob{}).
		Where("id = ? AND status IN ? AND available_at <= ?", candidate.ID, []string{StatusPending, StatusRetrying}, now).
		Updates(map[string]any{
			"status": StatusRunning, "attempts": gorm.Expr("attempts + 1"),
			"started_at": now, "locked_at": now, "finished_at": nil, "updated_at": now,
		})
	if claim.Error != nil {
		return false, fmt.Errorf("claim background job: %w", claim.Error)
	}
	if claim.RowsAffected == 0 {
		return false, nil
	}
	if err := q.db.WithContext(ctx).First(&candidate, candidate.ID).Error; err != nil {
		return true, fmt.Errorf("reload background job: %w", err)
	}
	hbCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go q.heartbeat(hbCtx, candidate.ID)

	q.mu.RLock()
	handler := q.handlers[candidate.Type]
	resultHandler := q.resultHandlers[candidate.Type]
	q.mu.RUnlock()
	if handler == nil && resultHandler == nil {
		return true, q.finishFailure(ctx, &candidate, fmt.Errorf("no handler registered for %s", candidate.Type))
	}
	payload, err := q.decrypt(candidate.Type, candidate.Payload)
	var result any
	if err == nil && resultHandler != nil {
		result, err = resultHandler(ctx, payload)
	} else if err == nil {
		err = handler(ctx, payload)
	}
	if err != nil {
		_ = q.finishFailure(ctx, &candidate, err)
		return true, err
	}
	finished := q.now()
	sealedResult := ""
	if result != nil {
		rawResult, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return true, q.finishFailure(ctx, &candidate, fmt.Errorf("marshal job result: %w", marshalErr))
		}
		sealedResult, err = q.encrypt(candidate.Type+".result", rawResult)
		if err != nil {
			return true, q.finishFailure(ctx, &candidate, err)
		}
	}
	err = q.db.WithContext(ctx).Model(&models.BackgroundJob{}).Where("id = ?", candidate.ID).Updates(map[string]any{
		"status": StatusSucceeded, "finished_at": finished, "locked_at": nil,
		"last_error": "", "result": sealedResult, "updated_at": finished,
	}).Error
	return true, err
}

// heartbeat 任务执行期间定期刷新 locked_at，避免长任务被其他进程当作中断而重复执行。
func (q *Queue) heartbeat(ctx context.Context, id uint) {
	every := q.heartbeatEvery
	if every <= 0 {
		every = defaultHeartbeat
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			q.db.Model(&models.BackgroundJob{}).Where("id = ? AND status = ?", id, StatusRunning).Update("locked_at", q.now())
		}
	}
}

func (q *Queue) Result(job *models.BackgroundJob, target any) error {
	if job == nil || strings.TrimSpace(job.Result) == "" {
		return errors.New("job result is not available")
	}
	raw, err := q.decrypt(job.Type+".result", job.Result)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

func (q *Queue) finishFailure(ctx context.Context, job *models.BackgroundJob, cause error) error {
	now := q.now()
	status := StatusRetrying
	available := now.Add(retryDelay(job.Attempts))
	var finished any
	if job.Attempts >= job.MaxAttempts {
		status = StatusFailed
		available = now
		finished = now
	}
	if err := q.db.WithContext(ctx).Model(&models.BackgroundJob{}).Where("id = ?", job.ID).Updates(map[string]any{
		"status": status, "available_at": available, "finished_at": finished,
		"locked_at": nil, "last_error": safeError(cause), "updated_at": now,
	}).Error; err != nil {
		return fmt.Errorf("persist job failure: %w", err)
	}
	return cause
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	shift := attempt - 1
	if shift > 7 {
		shift = 7
	}
	return time.Duration(5*(1<<shift)) * time.Second
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(strings.ReplaceAll(err.Error(), "\x00", ""))
	if len(message) > 1000 {
		message = message[:1000] + "…"
	}
	return message
}

func (q *Queue) RecoverStale(ctx context.Context) error {
	now := q.now()
	return q.db.WithContext(ctx).Model(&models.BackgroundJob{}).
		Where("status = ? AND locked_at < ?", StatusRunning, now.Add(-staleJobAge)).
		Updates(map[string]any{
			"status": StatusRetrying, "available_at": now, "locked_at": nil,
			"last_error": "任务执行被中断，已自动重新排队", "updated_at": now,
		}).Error
}

func (q *Queue) Retry(ctx context.Context, id uint) (bool, error) {
	now := q.now()
	result := q.db.WithContext(ctx).Model(&models.BackgroundJob{}).
		Where("id = ? AND status = ?", id, StatusFailed).
		Updates(map[string]any{
			"status": StatusPending, "attempts": 0, "available_at": now,
			"started_at": nil, "finished_at": nil, "locked_at": nil,
			"last_error": "", "result": "", "updated_at": now,
		})
	return result.RowsAffected > 0, result.Error
}

func (q *Queue) Start(ctx context.Context) {
	if err := q.RecoverStale(ctx); err != nil {
		log.Printf("[jobs] recover stale tasks failed: %v", err)
	}
	poll := time.NewTicker(2 * time.Second)
	cleanup := time.NewTicker(time.Hour)
	recoverTick := time.NewTicker(recoverEvery) // 多实例：其他实例退出后遗留的任务由仍在运行的实例接手
	defer poll.Stop()
	defer cleanup.Stop()
	defer recoverTick.Stop()
	for {
		ran, err := q.RunOnce(ctx)
		if err != nil {
			log.Printf("[jobs] task execution failed: %v", err)
		}
		if ran {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
		case <-recoverTick.C:
			if err := q.RecoverStale(ctx); err != nil {
				log.Printf("[jobs] recover stale tasks failed: %v", err)
			}
		case <-cleanup.C:
			cutoff := q.now().Add(-completedRetention)
			if err := q.db.WithContext(ctx).Where("status IN ? AND finished_at < ?", []string{StatusSucceeded, StatusFailed}, cutoff).
				Delete(&models.BackgroundJob{}).Error; err != nil {
				log.Printf("[jobs] cleanup completed tasks failed: %v", err)
			}
		}
	}
}

func (q *Queue) encrypt(jobType string, plaintext []byte) (string, error) {
	nonce := make([]byte, q.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("create job payload nonce: %w", err)
	}
	sealed := q.aead.Seal(nil, nonce, plaintext, []byte(jobType))
	return base64.RawStdEncoding.EncodeToString(append(nonce, sealed...)), nil
}

func (q *Queue) decrypt(jobType, encoded string) (json.RawMessage, error) {
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(raw) < q.aead.NonceSize() {
		return nil, errors.New("invalid encrypted job payload")
	}
	nonce, ciphertext := raw[:q.aead.NonceSize()], raw[q.aead.NonceSize():]
	plain, err := q.aead.Open(nil, nonce, ciphertext, []byte(jobType))
	if err != nil {
		return nil, errors.New("decrypt job payload failed")
	}
	return json.RawMessage(plain), nil
}
