package app

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/jobqueue"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// 「我的任务」：汇总用户发起的后台任务（核心的导入与外链图片本地化任务，及各插件登记的任务来源），
// 变化经用户的通知事件流（/notifications/stream）以 {"task": {...}} 推送。

const maxUserTasks = 50

// 核心任务在「我的任务」中的种类（前端据此显示文案）。
const (
	userTaskZIPImport     = "zipImport"
	userTaskPDFImport     = "pdfImport"
	userTaskImageLocalize = "imageLocalize"
)

var coreUserTaskKinds = map[string]string{
	zipImportJobType:      userTaskZIPImport,
	pdfImportJobType:      userTaskPDFImport,
	imageLocalizeJobType:  userTaskImageLocalize,
	storageMigrateJobType: userTaskStorageMigrate,
}

var coreJobStatusesByTab = map[string][]string{
	plugincore.UserTaskTabActive: {jobqueue.StatusPending, jobqueue.StatusRetrying, jobqueue.StatusRunning},
	plugincore.UserTaskTabDone:   {jobqueue.StatusSucceeded},
	plugincore.UserTaskTabFailed: {jobqueue.StatusFailed},
}

func init() {
	plugincore.RegisterUserTaskSource(plugincore.UserTaskSource{Kind: "core", List: func(core plugincore.Core, userID uint, tab string, limit int) []plugincore.UserTask {
		a, ok := core.(*App)
		if !ok {
			return nil
		}
		types := make([]string, 0, len(coreUserTaskKinds))
		for t := range coreUserTaskKinds {
			types = append(types, t)
		}
		var jobs []models.BackgroundJob
		a.DB.Where("owner_id = ? AND type IN ? AND status IN ?", userID, types, coreJobStatusesByTab[tab]).
			Order("created_at DESC, id DESC").Limit(limit).Find(&jobs)
		out := make([]plugincore.UserTask, 0, len(jobs))
		for i := range jobs {
			if t, ok := a.coreUserTask(&jobs[i]); ok {
				out = append(out, t)
			}
		}
		return out
	}})
}

// coreUserTask 核心后台任务的展示信息：名称取上传的文件名或书名，完成后链接到导入的书籍（或所处理书籍的管理页）。
func (a *App) coreUserTask(job *models.BackgroundJob) (plugincore.UserTask, bool) {
	kind, ok := coreUserTaskKinds[job.Type]
	queue := a.jobQueue()
	if !ok || queue == nil {
		return plugincore.UserTask{}, false
	}
	t := plugincore.UserTask{Kind: kind, ID: strconv.FormatUint(uint64(job.ID), 10), Error: job.LastError,
		CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt, FinishedAt: job.FinishedAt}
	switch job.Status {
	case jobqueue.StatusRunning:
		t.Status = plugincore.UserTaskRunning
	case jobqueue.StatusSucceeded:
		t.Status = plugincore.UserTaskDone
		t.Error = ""
	case jobqueue.StatusFailed:
		t.Status = plugincore.UserTaskFailed
	default:
		t.Status = plugincore.UserTaskQueued
	}
	var payload struct {
		BookID   uint   `json:"book_id"`
		Filename string `json:"filename"`
		Title    string `json:"title"`
	}
	_ = queue.Payload(job, &payload)
	t.Title = strings.TrimSpace(payload.Title)
	if t.Title == "" {
		t.Title = payload.Filename
	}
	var book models.Book
	if payload.BookID != 0 && a.DB.Select("id, slug, title").First(&book, payload.BookID).Error == nil {
		t.Title = book.Title
		if job.Type == imageLocalizeJobType {
			t.Link = "/book/settings/" + book.Slug + "/cleanup"
		} else {
			t.Link = "/book/settings/" + book.Slug + "/chapters"
		}
	}
	if job.Type == storageMigrateJobType {
		a.storageMigrationUserTask(&t, job.ID)
		return t, true
	}
	if job.Status == jobqueue.StatusSucceeded && job.Type != imageLocalizeJobType && job.Result != "" {
		var result struct {
			Book *struct {
				Slug  string `json:"slug"`
				Title string `json:"title"`
			} `json:"book"`
		}
		if queue.Result(job, &result) == nil && result.Book != nil && result.Book.Slug != "" {
			t.Title = result.Book.Title
			t.Link = "/book/detail/" + result.Book.Slug
		}
	}
	return t, true
}

// onBackgroundJobChange 核心后台任务状态变化时推送给发起人。
func (a *App) onBackgroundJobChange(job models.BackgroundJob) {
	if job.OwnerID == 0 {
		return
	}
	if t, ok := a.coreUserTask(&job); ok {
		a.PublishUserTask(job.OwnerID, t)
	}
}

// PublishUserTask 实现 plugincore 的任务推送：经用户的通知事件流送达（多实例时广播）。
func (a *App) PublishUserTask(userID uint, task plugincore.UserTask) {
	if a.Notifications == nil {
		return
	}
	if raw, err := json.Marshal(gin.H{"task": task}); err == nil {
		a.Notifications.broadcast(userID, string(raw))
	}
}

// ListMyTasks GET /users/me/tasks?tab=active|done|failed 当前用户的后台任务（各来源合并，按创建时间倒序）。
func (a *App) ListMyTasks(c *gin.Context) {
	tab := c.DefaultQuery("tab", plugincore.UserTaskTabActive)
	if _, ok := coreJobStatusesByTab[tab]; !ok {
		fail(c, http.StatusBadRequest, "任务分组无效")
		return
	}
	u := currentUser(c)
	items := []plugincore.UserTask{}
	for _, src := range plugincore.UserTaskSources() {
		if src.Plugin != "" && !a.pluginEnabled(src.Plugin) {
			continue
		}
		items = append(items, src.List(a, u.ID, tab, maxUserTasks)...)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	if len(items) > maxUserTasks {
		items = items[:maxUserTasks]
	}
	ok(c, gin.H{"items": items})
}
