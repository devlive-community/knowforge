package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/config"
	"knowforge/server/internal/i18ntext"
	"knowforge/server/internal/jobqueue"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/storage"
)

// —— 存储迁移：修改存储配置（如本地 → 阿里云 OSS）后，把已有文件搬到当前存储：从原存储读回 → 上传到当前存储 →
// 把正文、封面、头像、站点配置及插件登记的列中出现的旧地址改为新地址 → 更新文件记录 →（可选）删除原存储中的文件。
// 后台任务逐个文件处理，进度出现在「我的任务」中；单个文件失败不影响其他文件，可再次发起迁移处理剩余文件。——

const (
	storageMigrateJobType   = "storage.migrate"
	userTaskStorageMigrate  = "storageMigrate"
	cfgStorageMigration     = "storage_migration_state"
	maxStorageMigrateErrors = 20
)

func init() {
	// 核心表中可能含有上传文件地址的列（插件在各自的 init 中登记自己的列）
	plugincore.RegisterURLColumns("documents", "content")
	plugincore.RegisterURLColumns("document_revisions", "content")
	plugincore.RegisterURLColumns("books", "cover_image", "description")
	plugincore.RegisterURLColumns("users", "avatar")
	plugincore.RegisterURLColumns("comments", "content")
	plugincore.RegisterURLColumns("site_configs", "config_value")
	i18ntext.Register("notify.storage.migrated", map[string]string{
		"zh-CN": "存储迁移完成：成功 {done} 个，失败 {failed} 个",
		"en":    "Storage migration finished: {done} moved, {failed} failed",
	})
}

type storageMigrateJob struct {
	ActorID   uint `json:"actor_id"`
	DeleteOld bool `json:"delete_old"`
}

// storageMigrationState 最近一次迁移的进度（保存在站点配置中，页面与「我的任务」据此显示）。
type storageMigrationState struct {
	JobID        uint       `json:"job_id"`
	Status       string     `json:"status"` // running | done | failed
	FromDrivers  []string   `json:"from_drivers"`
	TargetDriver string     `json:"target_driver"`
	DeleteOld    bool       `json:"delete_old"`
	Total        int        `json:"total"`
	Done         int        `json:"done"`
	Failed       int        `json:"failed"`
	Errors       []string   `json:"errors"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
}

func (a *App) storageMigrationState() *storageMigrationState {
	raw := a.getSetting(cfgStorageMigration)
	if raw == "" {
		return nil
	}
	var s storageMigrationState
	if json.Unmarshal([]byte(raw), &s) != nil {
		return nil
	}
	return &s
}

func (a *App) saveStorageMigrationState(s *storageMigrationState) {
	raw, _ := json.Marshal(s)
	_ = a.setSetting(cfgStorageMigration, string(raw), "存储迁移：最近一次迁移的进度")
}

// migrationItem 一个待迁移的文件；FileID 为 0 表示本地上传目录中没有文件记录（早期上传）的文件。
type migrationItem struct {
	FileID uint
	Driver string
	Name   string
	URL    string
}

// storageMigrationItems 不在当前存储中的文件：有文件记录且驱动或地址前缀与当前存储不同的；
// 以及当前存储不是本地时，本地上传目录中没有文件记录、但仍被引用的文件。
func (a *App) storageMigrationItems() ([]migrationItem, storage.Uploader) {
	current := storage.Current(a.DB, config.DataDir())
	driver := storage.DriverName(current)
	base := storage.PublicBase(current)
	var files []models.UserFile
	a.DB.Order("id ASC").Find(&files)
	items := []migrationItem{}
	tracked := map[string]bool{}
	for _, f := range files {
		if f.Driver == "local" {
			tracked[f.Name] = true
		}
		if f.Driver == driver && base != "" && strings.HasPrefix(f.URL, base) {
			continue
		}
		items = append(items, migrationItem{FileID: f.ID, Driver: f.Driver, Name: f.Name, URL: f.URL})
	}
	if driver != "local" {
		names, _ := storage.LocalFiles(config.DataDir())
		for _, name := range names {
			if !tracked[name] && a.urlReferenced("/uploads/"+name) {
				items = append(items, migrationItem{Driver: "local", Name: name, URL: "/uploads/" + name})
			}
		}
	}
	return items, current
}

// urlColumnsInUse 已登记且表存在的列。
func (a *App) urlColumnsInUse() []plugincore.URLColumns {
	out := []plugincore.URLColumns{}
	for _, c := range plugincore.AllURLColumns() {
		if a.DB.Migrator().HasTable(c.Table) {
			out = append(out, c)
		}
	}
	return out
}

func (a *App) urlReferenced(fileURL string) bool {
	like := "%" + escapeLike(path.Base(fileURL)) + "%"
	for _, t := range a.urlColumnsInUse() {
		for _, col := range t.Columns {
			var n int64
			a.DB.Table(t.Table).Where(col+" LIKE ? ESCAPE '!'", like).Limit(1).Count(&n)
			if n > 0 {
				return true
			}
		}
	}
	return false
}

// urlPattern 匹配旧地址：本地文件的相对地址前面可能带有站点域名（如 https://kb.example.com/uploads/x.png），一并替换。
func urlPattern(oldURL string) *regexp.Regexp {
	if strings.HasPrefix(oldURL, "/") {
		return regexp.MustCompile(`(?:https?://[^\s"'()<>\[\]]+?)?` + regexp.QuoteMeta(oldURL))
	}
	return regexp.MustCompile(regexp.QuoteMeta(oldURL))
}

// rewriteFileURL 把登记的列中出现的旧地址改为新地址，返回改动的行数。
func (a *App) rewriteFileURL(oldURL, newURL string) int {
	re := urlPattern(oldURL)
	like := "%" + escapeLike(path.Base(oldURL)) + "%"
	changed := 0
	for _, t := range a.urlColumnsInUse() {
		for _, col := range t.Columns {
			var rows []struct {
				ID    uint
				Value string
			}
			a.DB.Table(t.Table).Select("id, "+col+" AS value").Where(col+" LIKE ? ESCAPE '!'", like).Scan(&rows)
			for _, r := range rows {
				if next := re.ReplaceAllLiteralString(r.Value, newURL); next != r.Value {
					if a.DB.Table(t.Table).Where("id = ?", r.ID).Update(col, next).Error == nil {
						changed++
					}
				}
			}
		}
	}
	return changed
}

// runStorageMigration 后台任务：逐个文件迁移到当前存储。
func (a *App) runStorageMigration(ctx context.Context, raw json.RawMessage) (any, error) {
	var job storageMigrateJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil, fmt.Errorf("解析存储迁移任务失败: %w", err)
	}
	items, current := a.storageMigrationItems()
	state := a.storageMigrationState()
	if state == nil || state.Status != "running" {
		state = &storageMigrationState{Status: "running", StartedAt: time.Now()}
	}
	state.TargetDriver = storage.DriverName(current)
	state.DeleteOld = job.DeleteOld
	state.Total, state.Done, state.Failed, state.Errors = len(items), 0, 0, []string{}
	from := map[string]bool{}
	for _, it := range items {
		from[it.Driver] = true
	}
	state.FromDrivers = state.FromDrivers[:0]
	for d := range from {
		state.FromDrivers = append(state.FromDrivers, d)
	}
	a.saveStorageMigrationState(state)
	a.publishStorageMigration(job.ActorID, state)

	fail := func(it migrationItem, err error) {
		state.Failed++
		if len(state.Errors) < maxStorageMigrateErrors {
			state.Errors = append(state.Errors, it.Name+": "+err.Error())
		}
	}
	for i, it := range items {
		if ctx.Err() != nil {
			break
		}
		data, err := storage.ReadFile(config.DataDir(), it.Driver, it.Name, it.URL)
		if err != nil {
			fail(it, err)
		} else if newURL, err := current.Upload(it.Name, data); err != nil {
			fail(it, err)
		} else {
			a.rewriteFileURL(it.URL, newURL)
			if it.FileID != 0 {
				a.DB.Model(&models.UserFile{}).Where("id = ?", it.FileID).Updates(map[string]any{"driver": storage.DriverName(current), "url": newURL})
			}
			if job.DeleteOld && it.URL != newURL {
				if old, ok := storage.ForDriver(a.DB, config.DataDir(), it.Driver).(storage.Deleter); ok {
					if err := old.Delete(it.Name); err != nil && len(state.Errors) < maxStorageMigrateErrors {
						state.Errors = append(state.Errors, it.Name+": 已迁移，但删除原文件失败："+err.Error())
					}
				} else if len(state.Errors) < maxStorageMigrateErrors {
					state.Errors = append(state.Errors, it.Name+": 已迁移，原存储配置已更改，原文件需在原存储中自行清理")
				}
			}
			state.Done++
		}
		if i%5 == 4 || i == len(items)-1 {
			a.saveStorageMigrationState(state)
			a.publishStorageMigration(job.ActorID, state)
		}
	}
	now := time.Now()
	state.FinishedAt = &now
	state.Status = "done"
	if state.Failed > 0 && state.Done == 0 {
		state.Status = "failed"
	}
	a.saveStorageMigrationState(state)
	a.publishStorageMigration(job.ActorID, state)
	a.NotifyI18n(job.ActorID, "system", "notify.storage.migrated",
		map[string]string{"done": strconv.Itoa(state.Done), "failed": strconv.Itoa(state.Failed)},
		map[string]any{"link": "/admin/settings/storage"})
	return gin.H{"total": state.Total, "done": state.Done, "failed": state.Failed}, nil
}

// storageMigrationUserTask 「我的任务」中的存储迁移（标题为源存储 → 目标存储，进度取自迁移进度）。
func (a *App) storageMigrationUserTask(t *plugincore.UserTask, jobID uint) {
	t.Link = "/admin/settings/storage"
	s := a.storageMigrationState()
	if s == nil || s.JobID != jobID {
		return
	}
	t.Title = strings.Join(s.FromDrivers, ", ") + " → " + s.TargetDriver
	t.Done, t.Failed, t.Total = s.Done, s.Failed, s.Total
}

func (a *App) publishStorageMigration(actorID uint, s *storageMigrationState) {
	if actorID == 0 || s.JobID == 0 {
		return
	}
	var job models.BackgroundJob
	if a.DB.First(&job, s.JobID).Error == nil {
		if t, ok := a.coreUserTask(&job); ok {
			if s.Status == "running" && t.Status != plugincore.UserTaskDone && t.Status != plugincore.UserTaskFailed {
				t.Status = plugincore.UserTaskRunning
			}
			a.PublishUserTask(actorID, t)
		}
	}
}

// AdminStorageMigration GET /storage/migration 不在当前存储中的文件数与最近一次迁移的进度。
func (a *App) AdminStorageMigration(c *gin.Context) {
	items, current := a.storageMigrationItems()
	byDriver := map[string]int{}
	for _, it := range items {
		byDriver[it.Driver]++
	}
	ok(c, gin.H{"target_driver": storage.DriverName(current), "pending": len(items), "by_driver": byDriver,
		"state": a.storageMigrationState(), "running": a.storageMigrationActive()})
}

func (a *App) storageMigrationActive() bool {
	var n int64
	a.DB.Model(&models.BackgroundJob{}).Where("type = ? AND status IN ?", storageMigrateJobType,
		[]string{jobqueue.StatusPending, jobqueue.StatusRunning, jobqueue.StatusRetrying}).Count(&n)
	return n > 0
}

// AdminStartStorageMigration POST /storage/migration {delete_old} 开始迁移（后台任务）；删除原文件时需要二次认证（开启时）。
func (a *App) AdminStartStorageMigration(c *gin.Context) {
	var req struct {
		DeleteOld bool `json:"delete_old"`
	}
	if c.ShouldBindJSON(&req) != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if req.DeleteOld && !a.requireStepUp(c, tfOpDelete) {
		return
	}
	if a.storageMigrationActive() {
		fail(c, http.StatusConflict, "已有存储迁移正在进行")
		return
	}
	items, current := a.storageMigrationItems()
	if len(items) == 0 {
		fail(c, http.StatusBadRequest, "所有文件都已在当前存储中，无需迁移")
		return
	}
	queue := a.jobQueue()
	if queue == nil {
		fail(c, http.StatusServiceUnavailable, "异步任务服务尚未就绪")
		return
	}
	u := currentUser(c)
	// 先写入进度（含任务 ID 之前的占位），任务入队后补上 ID，「我的任务」即可显示进度
	state := &storageMigrationState{Status: "running", TargetDriver: storage.DriverName(current), DeleteOld: req.DeleteOld, Total: len(items), StartedAt: time.Now(), Errors: []string{}}
	a.saveStorageMigrationState(state)
	job, err := queue.EnqueueOwned(c.Request.Context(), u.ID, storageMigrateJobType, storageMigrateJob{ActorID: u.ID, DeleteOld: req.DeleteOld}, 1)
	if err != nil {
		fail(c, http.StatusInternalServerError, "创建迁移任务失败")
		return
	}
	state.JobID = job.ID
	a.saveStorageMigrationState(state)
	a.recordAudit(c, "storage.migrated", "config", "storage", "存储迁移", map[string]any{"files": len(items), "target": state.TargetDriver, "delete_old": req.DeleteOld})
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": gin.H{"task": publicBackgroundJob(job), "pending": len(items)}})
}
