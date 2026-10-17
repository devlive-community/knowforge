package app

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"knowforge/server/internal/jobqueue"
	"knowforge/server/internal/models"

	"github.com/gin-gonic/gin"
)

type publicJob struct {
	ID          uint       `json:"id"`
	Type        string     `json:"type"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	MaxAttempts int        `json:"max_attempts"`
	AvailableAt time.Time  `json:"available_at"`
	StartedAt   *time.Time `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
	LastError   string     `json:"last_error"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	Result      any        `json:"result,omitempty"`
}

func publicBackgroundJob(job *models.BackgroundJob) publicJob {
	return publicJob{ID: job.ID, Type: job.Type, Status: job.Status, Attempts: job.Attempts, MaxAttempts: job.MaxAttempts,
		AvailableAt: job.AvailableAt, StartedAt: job.StartedAt, FinishedAt: job.FinishedAt, LastError: job.LastError,
		CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt}
}

// GetBackgroundJob GET /tasks/:id 仅返回当前用户自己的任务；payload 永不返回。
func (a *App) GetBackgroundJob(c *gin.Context) {
	id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id64 == 0 {
		fail(c, http.StatusBadRequest, "任务 ID 无效")
		return
	}
	u := currentUser(c)
	var job models.BackgroundJob
	if err := a.DB.Where("id = ? AND owner_id = ?", uint(id64), u.ID).First(&job).Error; err != nil {
		fail(c, http.StatusNotFound, "任务不存在")
		return
	}
	response := publicBackgroundJob(&job)
	if job.Status == jobqueue.StatusSucceeded && job.Result != "" {
		queue := a.jobQueue()
		if queue == nil {
			fail(c, http.StatusServiceUnavailable, "异步任务服务尚未就绪")
			return
		}
		var result json.RawMessage
		if err := queue.Result(&job, &result); err != nil {
			fail(c, http.StatusInternalServerError, "读取任务结果失败")
			return
		}
		response.Result = result
	}
	ok(c, response)
}

var backgroundJobStatuses = map[string]bool{
	jobqueue.StatusPending: true, jobqueue.StatusRunning: true, jobqueue.StatusRetrying: true,
	jobqueue.StatusSucceeded: true, jobqueue.StatusFailed: true, jobqueue.StatusCanceled: true,
}

// AdminListBackgroundJobs GET /admin/tasks 管理员分页查看任务状态；加密 payload 永不返回。
func (a *App) AdminListBackgroundJobs(c *gin.Context) {
	page, pageSize := paginate(c)
	query := a.DB.Model(&models.BackgroundJob{})
	if jobType := strings.TrimSpace(c.Query("type")); jobType != "" {
		query = query.Where("type = ?", jobType)
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		if !backgroundJobStatuses[status] {
			fail(c, http.StatusBadRequest, "任务状态无效")
			return
		}
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询异步任务失败")
		return
	}
	items := []models.BackgroundJob{}
	if err := query.Order("created_at DESC, id DESC").Limit(pageSize).Offset((page - 1) * pageSize).Find(&items).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询异步任务失败")
		return
	}
	ok(c, PageResult{Items: items, Total: total, Page: page, PageSize: pageSize})
}

// AdminRetryBackgroundJob POST /admin/tasks/:id/retry 只允许重新排队最终失败或已取消的任务。
func (a *App) AdminRetryBackgroundJob(c *gin.Context) {
	id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id64 == 0 {
		fail(c, http.StatusBadRequest, "任务 ID 无效")
		return
	}
	queue := a.jobQueue()
	if queue == nil {
		fail(c, http.StatusServiceUnavailable, "异步任务服务尚未就绪")
		return
	}
	retried, err := queue.Retry(c.Request.Context(), uint(id64))
	if err != nil {
		fail(c, http.StatusInternalServerError, "重新排队失败")
		return
	}
	if !retried {
		fail(c, http.StatusConflict, "仅失败或已取消的任务可以重新排队")
		return
	}
	a.recordAudit(c, "task.retried", "task", strconv.FormatUint(id64, 10), "异步任务", map[string]any{"task_id": id64})
	ok(c, gin.H{"id": id64, "status": jobqueue.StatusPending})
}

// AdminCancelBackgroundJob POST /admin/tasks/:id/cancel 取消等待中或执行中的任务（如卡住的整站采集）。
func (a *App) AdminCancelBackgroundJob(c *gin.Context) {
	id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id64 == 0 {
		fail(c, http.StatusBadRequest, "任务 ID 无效")
		return
	}
	queue := a.jobQueue()
	if queue == nil {
		fail(c, http.StatusServiceUnavailable, "异步任务服务尚未就绪")
		return
	}
	canceled, err := queue.Cancel(c.Request.Context(), uint(id64), "已被管理员取消")
	if err != nil {
		fail(c, http.StatusInternalServerError, "取消任务失败")
		return
	}
	if !canceled {
		fail(c, http.StatusConflict, "仅等待中或执行中的任务可以取消")
		return
	}
	a.recordAudit(c, "task.canceled", "task", strconv.FormatUint(id64, 10), "异步任务", map[string]any{"task_id": id64})
	ok(c, gin.H{"id": id64, "status": jobqueue.StatusCanceled})
}

// AdminBackgroundJobTypes GET /admin/tasks/types 已注册的任务类型（核心与插件），供管理端按类型筛选。
func (a *App) AdminBackgroundJobTypes(c *gin.Context) {
	types := []string{}
	if queue := a.jobQueue(); queue != nil {
		types = queue.Types()
	}
	ok(c, gin.H{"items": types})
}
