package jobqueue

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"knowforge/server/internal/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func testQueue(t *testing.T) (*Queue, *gorm.DB, *time.Time) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.BackgroundJob{}); err != nil {
		t.Fatal(err)
	}
	queue, err := New(db, "test-job-secret")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	queue.now = func() time.Time { return now }
	return queue, db, &now
}

func TestQueueEncryptsPayloadAndRetriesUntilSuccess(t *testing.T) {
	queue, db, now := testQueue(t)
	type payload struct {
		Email string `json:"email"`
	}
	calls := 0
	queue.Register("email.send", func(_ context.Context, raw json.RawMessage) error {
		calls++
		var value payload
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		if value.Email != "reader@example.com" {
			t.Fatalf("unexpected payload: %+v", value)
		}
		if calls < 3 {
			return errors.New("temporary SMTP failure")
		}
		return nil
	})

	job, err := queue.Enqueue(context.Background(), "email.send", payload{Email: "reader@example.com"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(job.Payload, "reader@example.com") {
		t.Fatal("job payload must be encrypted at rest")
	}

	for attempt, advance := range []time.Duration{0, 6 * time.Second, 11 * time.Second} {
		*now = now.Add(advance)
		ran, runErr := queue.RunOnce(context.Background())
		if !ran {
			t.Fatalf("attempt %d did not claim a job", attempt+1)
		}
		if attempt < 2 && runErr == nil {
			t.Fatalf("attempt %d should fail", attempt+1)
		}
		if attempt == 2 && runErr != nil {
			t.Fatalf("final attempt failed: %v", runErr)
		}
	}

	var stored models.BackgroundJob
	if err := db.First(&stored, job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusSucceeded || stored.Attempts != 3 || stored.FinishedAt == nil || stored.LastError != "" {
		t.Fatalf("unexpected completed job: %+v", stored)
	}
}

func TestQueuePersistsFailureAndAllowsManualRetry(t *testing.T) {
	queue, db, now := testQueue(t)
	queue.Register("maintenance.test", func(context.Context, json.RawMessage) error {
		return errors.New("database unavailable")
	})
	job, err := queue.Enqueue(context.Background(), "maintenance.test", map[string]string{"scope": "expired"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if ran, runErr := queue.RunOnce(context.Background()); !ran || runErr == nil {
		t.Fatalf("expected failed execution, ran=%v err=%v", ran, runErr)
	}

	var failed models.BackgroundJob
	if err := db.First(&failed, job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if failed.Status != StatusFailed || failed.Attempts != 1 || failed.LastError == "" {
		t.Fatalf("unexpected failed job: %+v", failed)
	}

	*now = now.Add(time.Minute)
	retried, err := queue.Retry(context.Background(), job.ID)
	if err != nil || !retried {
		t.Fatalf("retry failed: retried=%v err=%v", retried, err)
	}
	if retriedAgain, err := queue.Retry(context.Background(), job.ID); err != nil || retriedAgain {
		t.Fatalf("non-failed task must not be queued twice: retried=%v err=%v", retriedAgain, err)
	}
}

func TestQueueStoresEncryptedResultForOwner(t *testing.T) {
	queue, db, _ := testQueue(t)
	queue.RegisterResult("content.import", func(_ context.Context, raw json.RawMessage) (any, error) {
		return map[string]any{"book_id": 42, "message": "import completed"}, nil
	})
	job, err := queue.EnqueueOwned(context.Background(), 7, "content.import", map[string]string{"path": "private.pdf"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if ran, runErr := queue.RunOnce(context.Background()); !ran || runErr != nil {
		t.Fatalf("result job failed: ran=%v err=%v", ran, runErr)
	}
	var stored models.BackgroundJob
	if err := db.First(&stored, job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.OwnerID != 7 || stored.Result == "" || strings.Contains(stored.Result, "import completed") {
		t.Fatalf("job result must be encrypted and owned: %+v", stored)
	}
	var result map[string]any
	if err := queue.Result(&stored, &result); err != nil {
		t.Fatal(err)
	}
	if result["book_id"] != float64(42) || result["message"] != "import completed" {
		t.Fatalf("unexpected decrypted result: %+v", result)
	}
}

func TestQueueRecoversStaleRunningJob(t *testing.T) {
	queue, db, now := testQueue(t)
	locked := now.Add(-10 * time.Minute)
	job := models.BackgroundJob{
		Type: "maintenance.test", Payload: "encrypted", Status: StatusRunning,
		Attempts: 1, MaxAttempts: 3, AvailableAt: locked, LockedAt: &locked,
		CreatedAt: locked, UpdatedAt: locked,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.RecoverStale(context.Background()); err != nil {
		t.Fatal(err)
	}
	var recovered models.BackgroundJob
	if err := db.First(&recovered, job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if recovered.Status != StatusRetrying || recovered.LockedAt != nil || recovered.AvailableAt.Before(*now) {
		t.Fatalf("stale job was not recovered: %+v", recovered)
	}
}

func TestQueueEnqueueIfDueSuppressesActiveAndRecentTasks(t *testing.T) {
	queue, db, now := testQueue(t)
	ctx := context.Background()

	first, created, err := queue.EnqueueIfDue(ctx, "maintenance.cleanup", map[string]any{}, 3, 24*time.Hour)
	if err != nil || !created || first == nil {
		t.Fatalf("first due task was not created: created=%v job=%v err=%v", created, first, err)
	}
	if duplicate, created, err := queue.EnqueueIfDue(ctx, "maintenance.cleanup", map[string]any{}, 3, 24*time.Hour); err != nil || created || duplicate != nil {
		t.Fatalf("active task must suppress duplicate: created=%v job=%v err=%v", created, duplicate, err)
	}

	finished := *now
	if err := db.Model(&models.BackgroundJob{}).Where("id = ?", first.ID).Updates(map[string]any{
		"status": StatusSucceeded, "finished_at": finished,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if recent, created, err := queue.EnqueueIfDue(ctx, "maintenance.cleanup", map[string]any{}, 3, 24*time.Hour); err != nil || created || recent != nil {
		t.Fatalf("recent successful task must suppress duplicate: created=%v job=%v err=%v", created, recent, err)
	}

	*now = now.Add(25 * time.Hour)
	next, created, err := queue.EnqueueIfDue(ctx, "maintenance.cleanup", map[string]any{}, 3, 24*time.Hour)
	if err != nil || !created || next == nil || next.ID == first.ID {
		t.Fatalf("task should become due after cooldown: created=%v job=%v err=%v", created, next, err)
	}
}

// 长任务执行期间持续心跳：即使超过 staleJobAge，其他进程的恢复也不会把它当作中断而重复执行。
func TestQueueHeartbeatKeepsLongJobClaimed(t *testing.T) {
	queue, db, start := testQueue(t)
	var clock atomic.Int64
	clock.Store(start.UnixNano())
	queue.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	queue.heartbeatEvery = 10 * time.Millisecond

	started, release := make(chan struct{}), make(chan struct{})
	queue.Register("long.test", func(context.Context, json.RawMessage) error {
		close(started)
		<-release
		return nil
	})
	if _, err := queue.Enqueue(context.Background(), "long.test", map[string]string{}, 3); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := queue.RunOnce(context.Background())
		done <- err
	}()
	<-started
	clock.Add(int64(10 * time.Minute)) // 已执行 10 分钟
	time.Sleep(60 * time.Millisecond)  // 期间心跳刷新了 locked_at
	if err := queue.RecoverStale(context.Background()); err != nil {
		t.Fatal(err)
	}
	var job models.BackgroundJob
	db.Where("type = ?", "long.test").First(&job)
	if job.Status != StatusRunning {
		t.Fatalf("有心跳的任务不应被恢复: %+v", job)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	db.First(&job, job.ID)
	if job.Status != StatusSucceeded || job.Attempts != 1 {
		t.Fatalf("任务应只执行一次并成功: %+v", job)
	}
}
