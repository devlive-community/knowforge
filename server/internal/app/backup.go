package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/cluster"
	"knowforge/server/internal/config"
	"knowforge/server/internal/i18ntext"
	"knowforge/server/internal/jobqueue"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// —— 站点备份：管理员一键备份或按计划自动备份（每天 / 每周的指定时间，按服务器时区），自动备份保留最近若干份。
// 备份文件保存在数据目录 backups/ 下，包含站点密钥、密码哈希与接口密钥，下载需要短时凭证（开启二次认证时先验证）。
// 恢复在安装向导中进行（见 backup_restore.go）。——

const (
	backupJobType       = "system.backup"
	userTaskBackup      = "backup"
	cfgBackupSettings   = "backup_settings"
	backupTicketTTL     = 5 * time.Minute
	backupTicketPrefix  = "knowforge-backup-download:"
	backupCheckInterval = 10 * time.Minute
)

func init() {
	coreUserTaskKinds[backupJobType] = userTaskBackup
	i18ntext.Register("notify.backup.done", map[string]string{
		"zh-CN": "站点备份完成：{name}（{size}）",
		"en":    "Site backup finished: {name} ({size})",
	})
	i18ntext.Register("notify.backup.failed", map[string]string{
		"zh-CN": "站点备份失败：{error}",
		"en":    "Site backup failed: {error}",
	})
}

// backupSettings 自动备份设置。
type backupSettings struct {
	Schedule     string `json:"schedule"` // off | daily | weekly
	Hour         int    `json:"hour"`     // 0-23（服务器时区）
	Weekday      int    `json:"weekday"`  // 0-6（周日为 0），每周备份时使用
	Keep         int    `json:"keep"`     // 自动备份保留份数
	IncludeFiles bool   `json:"include_files"`
}

func defaultBackupSettings() backupSettings {
	return backupSettings{Schedule: "off", Hour: 3, Weekday: 0, Keep: 7, IncludeFiles: true}
}

func (a *App) backupSettings() backupSettings {
	s := defaultBackupSettings()
	if raw := a.getSetting(cfgBackupSettings); raw != "" {
		_ = json.Unmarshal([]byte(raw), &s)
	}
	return s
}

func backupDir() string { return filepath.Join(config.DataDir(), "backups") }

// backupPath 备份文件路径（名称只允许本系统生成的文件名，防止越出目录）。
func backupPath(name string) (string, bool) {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".zip") {
		return "", false
	}
	return filepath.Join(backupDir(), name), true
}

// backupRunning 是否有备份任务在排队或执行（以任务队列为准，服务重启遗留的「进行中」记录不算）。
func (a *App) backupRunning() bool {
	var n int64
	a.DB.Model(&models.BackgroundJob{}).Where("type = ? AND status IN ?", backupJobType,
		[]string{jobqueue.StatusPending, jobqueue.StatusRunning, jobqueue.StatusRetrying}).Count(&n)
	return n > 0
}

type backupJob struct {
	BackupID uint `json:"backup_id"`
	ActorID  uint `json:"actor_id"`
}

// startBackup 新建备份记录并排队（manual 由管理员发起，scheduled 由计划触发）。
func (a *App) startBackup(ctx context.Context, kind string, actorID uint, includeFiles bool) (*models.SystemBackup, *models.BackgroundJob, error) {
	queue := a.jobQueue()
	if queue == nil {
		return nil, nil, errors.New("异步任务服务尚未就绪")
	}
	now := currentTime()
	b := models.SystemBackup{
		Name: fmt.Sprintf("knowforge-backup-%s-%s.zip", now.Format("20060102-150405"), strings.ReplaceAll(Version, "/", "-")),
		Kind: kind, Status: "running", IncludeFiles: includeFiles, Stage: "database", AppVersion: Version, CreatedBy: actorID,
	}
	if err := a.DB.Create(&b).Error; err != nil {
		return nil, nil, err
	}
	payload := backupJob{BackupID: b.ID, ActorID: actorID}
	var job *models.BackgroundJob
	var err error
	if actorID != 0 {
		job, err = queue.EnqueueOwned(ctx, actorID, backupJobType, payload, 1)
	} else {
		job, err = queue.Enqueue(ctx, backupJobType, payload, 1)
	}
	if err != nil {
		a.DB.Delete(&b)
		return nil, nil, err
	}
	return &b, job, nil
}

// runBackupJob 后台任务：写出备份文件（先写入临时文件，完成后改名），更新进度；自动备份完成后清理超出保留份数的旧备份。
func (a *App) runBackupJob(ctx context.Context, raw json.RawMessage) (any, error) {
	var job backupJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil, fmt.Errorf("解析备份任务失败: %w", err)
	}
	var b models.SystemBackup
	if err := a.DB.First(&b, job.BackupID).Error; err != nil {
		return nil, fmt.Errorf("备份记录不存在: %w", err)
	}
	b.Status, b.Error, b.FinishedAt = "running", "", nil // 任务重试时重新写出
	finish := func(err error) error {
		now := currentTime()
		b.FinishedAt = &now
		if err != nil {
			b.Status, b.Error = "failed", err.Error()
		} else {
			b.Status, b.Error = "done", ""
		}
		a.DB.Save(&b)
		a.publishBackup(job.ActorID, &b)
		a.notifyBackup(job.ActorID, &b)
		return err
	}
	if err := os.MkdirAll(backupDir(), 0o755); err != nil {
		return nil, finish(fmt.Errorf("创建备份目录失败: %w", err))
	}
	final, _ := backupPath(b.Name)
	partial := final + ".part"
	f, err := os.Create(partial)
	if err != nil {
		return nil, finish(fmt.Errorf("创建备份文件失败: %w", err))
	}
	last := time.Time{}
	progress := func(stage string, done, total int) {
		b.Stage, b.Done, b.Total = stage, done, total
		if time.Since(last) > time.Second || done == total {
			last = time.Now()
			a.DB.Model(&models.SystemBackup{}).Where("id = ?", b.ID).Updates(map[string]any{"stage": stage, "done": done, "total": total})
			a.publishBackup(job.ActorID, &b)
		}
	}
	stats, err := a.writeBackupArchive(ctx, f, b.IncludeFiles, progress)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(partial, final)
	}
	if err != nil {
		_ = os.Remove(partial)
		return nil, finish(err)
	}
	if st, err := os.Stat(final); err == nil {
		b.Size = st.Size()
	}
	b.Tables, b.Rows, b.Files = stats.Tables, stats.Rows, stats.Files
	_ = finish(nil)
	if b.Kind == "scheduled" {
		a.pruneScheduledBackups(a.backupSettings().Keep)
	}
	return gin.H{"name": b.Name, "size": b.Size, "tables": b.Tables, "rows": b.Rows, "files": b.Files}, nil
}

// pruneScheduledBackups 自动备份只保留最近 keep 份（手动备份不自动删除）。
func (a *App) pruneScheduledBackups(keep int) {
	if keep < 1 {
		keep = 1
	}
	var old []models.SystemBackup
	a.DB.Where("kind = ? AND status = ?", "scheduled", "done").Order("created_at DESC, id DESC").Offset(keep).Find(&old)
	for i := range old {
		a.removeBackup(&old[i])
	}
}

func (a *App) removeBackup(b *models.SystemBackup) {
	if p, ok := backupPath(b.Name); ok {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			log.Printf("[backup] 删除备份文件失败 %s: %v", b.Name, err)
		}
		_ = os.Remove(p + ".part")
	}
	a.DB.Delete(b)
}

func (a *App) notifyBackup(actorID uint, b *models.SystemBackup) {
	recipients := []uint{}
	if actorID != 0 {
		recipients = append(recipients, actorID)
	} else if b.Status == "failed" {
		// 自动备份失败时通知全部管理员
		a.DB.Model(&models.User{}).Where("role = ? AND is_active = ?", "admin", true).Pluck("id", &recipients)
	}
	for _, uid := range recipients {
		if b.Status == "done" {
			a.NotifyI18n(uid, "system", "notify.backup.done", map[string]string{"name": b.Name, "size": formatBytes(b.Size)}, map[string]any{"link": "/admin/settings/backup"})
		} else {
			a.NotifyI18n(uid, "system", "notify.backup.failed", map[string]string{"error": b.Error}, map[string]any{"link": "/admin/settings/backup"})
		}
	}
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// backupUserTask 「我的任务」中的备份：标题为备份文件名，进度取自备份记录。
func (a *App) backupUserTask(t *plugincore.UserTask, job *models.BackgroundJob) {
	t.Link = "/admin/settings/backup"
	queue := a.jobQueue()
	var payload backupJob
	if queue == nil || queue.Payload(job, &payload) != nil {
		return
	}
	var b models.SystemBackup
	if a.DB.First(&b, payload.BackupID).Error != nil {
		return
	}
	t.Title = b.Name
	t.Done, t.Total = b.Done, b.Total
}

func (a *App) publishBackup(actorID uint, b *models.SystemBackup) {
	if actorID == 0 {
		return
	}
	var job models.BackgroundJob
	if a.DB.Where("type = ? AND owner_id = ?", backupJobType, actorID).Order("id DESC").First(&job).Error != nil {
		return
	}
	if t, ok := a.coreUserTask(&job); ok {
		t.Title, t.Done, t.Total = b.Name, b.Done, b.Total
		switch b.Status {
		case "running":
			t.Status = plugincore.UserTaskRunning
		case "done":
			t.Status = plugincore.UserTaskDone
		case "failed":
			t.Status, t.Error = plugincore.UserTaskFailed, b.Error
		}
		a.PublishUserTask(actorID, t)
	}
}

// enqueueBackupIfDue 按计划触发自动备份：到达设定的小时（每周备份还需是设定的星期几），且 20 小时内没有自动备份时排队。
// 每 10 分钟检查一次；多实例时只由持有「备份计划」租约的实例检查。
func (a *App) enqueueBackupIfDue(ctx context.Context) {
	s := a.backupSettings()
	if s.Schedule != "daily" && s.Schedule != "weekly" {
		return
	}
	now := currentTime()
	if now.Hour() != s.Hour || (s.Schedule == "weekly" && int(now.Weekday()) != s.Weekday) {
		return
	}
	if !cluster.TryLease("backup-schedule", backupCheckInterval+5*time.Minute) {
		return
	}
	var recent int64
	a.DB.Model(&models.SystemBackup{}).Where("kind = ? AND created_at > ?", "scheduled", now.Add(-20*time.Hour)).Count(&recent)
	if recent > 0 || a.backupRunning() {
		return
	}
	if _, _, err := a.startBackup(ctx, "scheduled", 0, s.IncludeFiles); err != nil {
		log.Printf("[backup] 自动备份排队失败: %v", err)
	}
}

// —— 下载凭证：浏览器直接下载大文件无法携带请求头，凭证为 HMAC 签名的「备份 ID + 用户 + 过期时间」，5 分钟内有效。——

func (a *App) backupTicketMAC(payload string) string {
	mac := hmac.New(sha256.New, []byte(a.Config.Secret))
	mac.Write([]byte(backupTicketPrefix + payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *App) issueBackupTicket(backupID, userID uint, now time.Time) string {
	payload := fmt.Sprintf("%d.%d.%d", backupID, userID, now.Add(backupTicketTTL).Unix())
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + a.backupTicketMAC(payload)
}

func (a *App) parseBackupTicket(ticket string, backupID uint, now time.Time) bool {
	encoded, sig, ok := strings.Cut(ticket, ".")
	if !ok {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || !hmac.Equal([]byte(sig), []byte(a.backupTicketMAC(string(raw)))) {
		return false
	}
	parts := strings.Split(string(raw), ".")
	if len(parts) != 3 || parts[0] != strconv.FormatUint(uint64(backupID), 10) {
		return false
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	return err == nil && now.Unix() <= exp
}

// —— 接口 ——

// AdminListBackups GET /backups?page=&page_size= 备份列表（新的在前）、自动备份设置与服务器时间。
func (a *App) AdminListBackups(c *gin.Context) {
	a.markInterruptedBackups()
	page, size := paginate(c)
	var total int64
	a.DB.Model(&models.SystemBackup{}).Count(&total)
	items := []models.SystemBackup{}
	a.DB.Order("created_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&items)
	now := currentTime()
	zone, offset := now.Zone()
	ok(c, gin.H{
		"items": items, "total": total, "page": page, "page_size": size,
		"settings": a.backupSettings(), "running": a.backupRunning(),
		"server_time": now.Format(time.RFC3339), "server_zone": zone, "server_offset": offset,
		"db_type": a.DB.Dialector.Name(), "data_dir": backupDir(),
	})
}

// AdminCreateBackup POST /backups {include_files} 立即备份（后台任务，进度见「我的任务」与备份页）。
func (a *App) AdminCreateBackup(c *gin.Context) {
	var req struct {
		IncludeFiles bool `json:"include_files"`
	}
	if c.ShouldBindJSON(&req) != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if a.backupRunning() {
		fail(c, http.StatusConflict, "已有备份正在进行")
		return
	}
	u := currentUser(c)
	b, job, err := a.startBackup(c.Request.Context(), "manual", u.ID, req.IncludeFiles)
	if err != nil {
		fail(c, http.StatusInternalServerError, "创建备份任务失败："+err.Error())
		return
	}
	a.recordAudit(c, "backup.created", "backup", strconv.FormatUint(uint64(b.ID), 10), b.Name, map[string]any{"include_files": req.IncludeFiles})
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": gin.H{"backup": b, "task": publicBackgroundJob(job)}})
}

// AdminSaveBackupSettings PUT /backups/settings {schedule, hour, weekday, keep, include_files}
func (a *App) AdminSaveBackupSettings(c *gin.Context) {
	var s backupSettings
	if c.ShouldBindJSON(&s) != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if s.Schedule != "off" && s.Schedule != "daily" && s.Schedule != "weekly" {
		fail(c, http.StatusBadRequest, "备份频率无效")
		return
	}
	if s.Hour < 0 || s.Hour > 23 || s.Weekday < 0 || s.Weekday > 6 || s.Keep < 1 || s.Keep > 100 {
		fail(c, http.StatusBadRequest, "备份时间或保留份数无效（保留 1-100 份）")
		return
	}
	raw, _ := json.Marshal(s)
	if err := a.setSetting(cfgBackupSettings, string(raw), "站点备份：自动备份设置"); err != nil {
		fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	a.recordAudit(c, "backup.settings", "config", "backup", "自动备份设置", map[string]any{"schedule": s.Schedule, "hour": s.Hour, "weekday": s.Weekday, "keep": s.Keep, "include_files": s.IncludeFiles})
	ok(c, s)
}

func (a *App) findBackup(c *gin.Context) *models.SystemBackup {
	var b models.SystemBackup
	if a.DB.First(&b, c.Param("id")).Error != nil {
		fail(c, http.StatusNotFound, "备份不存在")
		return nil
	}
	return &b
}

// AdminBackupDownloadTicket POST /backups/:id/download-ticket 下载凭证（开启二次认证时需先验证「导出数据」），返回下载地址。
func (a *App) AdminBackupDownloadTicket(c *gin.Context) {
	b := a.findBackup(c)
	if b == nil {
		return
	}
	if b.Status != "done" {
		fail(c, http.StatusBadRequest, "备份尚未完成")
		return
	}
	if p, _ := backupPath(b.Name); !fileExists(p) {
		fail(c, http.StatusNotFound, "备份文件不在本服务器上（可能已被删除，或位于其他实例的数据目录）")
		return
	}
	if !a.requireStepUp(c, tfOpUnbindExport) {
		return
	}
	u := currentUser(c)
	a.recordAudit(c, "backup.downloaded", "backup", strconv.FormatUint(uint64(b.ID), 10), b.Name, nil)
	ticket := a.issueBackupTicket(b.ID, u.ID, time.Now())
	ok(c, gin.H{"url": fmt.Sprintf("/api/v1/backups/%d/download?ticket=%s", b.ID, ticket), "expires_in": int(backupTicketTTL.Seconds())})
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// DownloadBackup GET /backups/:id/download?ticket= 凭下载凭证下载备份文件（不需要登录请求头）。
func (a *App) DownloadBackup(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	if !a.parseBackupTicket(c.Query("ticket"), uint(id), time.Now()) {
		fail(c, http.StatusForbidden, "下载链接无效或已过期，请回到备份页面重新下载")
		return
	}
	var b models.SystemBackup
	if a.DB.First(&b, uint(id)).Error != nil {
		fail(c, http.StatusNotFound, "备份不存在")
		return
	}
	p, valid := backupPath(b.Name)
	if !valid || !fileExists(p) {
		fail(c, http.StatusNotFound, "备份文件不存在")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.FileAttachment(p, b.Name)
}

// AdminDeleteBackup DELETE /backups/:id 删除备份（开启二次认证时需先验证「删除」）。
func (a *App) AdminDeleteBackup(c *gin.Context) {
	b := a.findBackup(c)
	if b == nil {
		return
	}
	if b.Status == "running" {
		fail(c, http.StatusConflict, "备份正在进行，完成后才能删除")
		return
	}
	if !a.requireStepUp(c, tfOpDelete) {
		return
	}
	a.removeBackup(b)
	a.recordAudit(c, "backup.deleted", "backup", strconv.FormatUint(uint64(b.ID), 10), b.Name, nil)
	ok(c, gin.H{"id": b.ID})
}

// markInterruptedBackups 仍为「进行中」但已没有备份任务在运行的记录（服务在备份过程中重启）标记为失败。
func (a *App) markInterruptedBackups() {
	if a.backupRunning() {
		return
	}
	now := currentTime()
	a.DB.Model(&models.SystemBackup{}).Where("status = ?", "running").
		Updates(map[string]any{"status": "failed", "error": "备份被中断（服务重启），请重新备份", "finished_at": now})
}
