package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"knowforge/server/internal/cluster"
	"knowforge/server/internal/config"
	"knowforge/server/internal/jobqueue"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

const (
	emailSendJobType      = "email.send"
	pdfImportJobType      = "content.import.pdf"
	zipImportJobType      = "content.import.zip"
	maintenanceJobType    = "maintenance.cleanup"
	importSourceRetention = 30 * 24 * time.Hour
	maintenanceInterval   = 24 * time.Hour
	maintenanceCheckEvery = time.Hour
)

type emailSendJob struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	HTML    string `json:"html"`
}

type pdfImportJob struct {
	UserID     uint   `json:"user_id"`
	BookID     uint   `json:"book_id,omitempty"`
	Mode       string `json:"mode,omitempty"`
	SourcePath string `json:"source_path"`
	Filename   string `json:"filename"`
	Title      string `json:"title,omitempty"`
}

type zipImportJob struct {
	UserID     uint   `json:"user_id"`
	SourcePath string `json:"source_path"`
	Filename   string `json:"filename"`
	Size       int64  `json:"size"`
	Title      string `json:"title,omitempty"`
}

func (a *App) configureJobQueue() error {
	queue, err := jobqueue.New(a.DB, a.Config.Secret)
	if err != nil {
		return err
	}
	queue.Register(emailSendJobType, func(_ context.Context, raw json.RawMessage) error {
		var job emailSendJob
		if err := json.Unmarshal(raw, &job); err != nil {
			return fmt.Errorf("解析邮件任务失败: %w", err)
		}
		if job.To == "" || job.Subject == "" || job.HTML == "" {
			return fmt.Errorf("邮件任务缺少必要字段")
		}
		return a.mailSender().Send(job.To, job.Subject, job.HTML)
	})
	queue.OnChange(a.onBackgroundJobChange)
	queue.RegisterResult(pdfImportJobType, a.runPDFImportJob)
	queue.RegisterResult(zipImportJobType, a.runZIPImportJob)
	queue.RegisterResult(imageLocalizeJobType, a.runImageLocalizeJob)
	queue.RegisterResult(storageMigrateJobType, a.runStorageMigration)
	queue.RegisterResult(backupJobType, a.runBackupJob)
	queue.Register(maintenanceJobType, a.runMaintenanceCleanup)
	queue.Register(sitemapJobType, a.runSitemapGenerate)
	queue.Register(notificationBackfillJobType, a.runNotificationBackfill)
	// 插件登记的后台任务（如内容采集插件的整站采集）
	for _, job := range plugincore.Jobs() {
		queue.Register(job.Type, job.Factory(a))
	}
	a.jobsMu.Lock()
	a.Jobs = queue
	a.jobsMu.Unlock()
	plugincore.FireJobQueueSweep(a, queue)
	return nil
}

func (a *App) runMaintenanceCleanup(ctx context.Context, _ json.RawMessage) error {
	now := currentTime()
	if err := purgeExpiredBookAnalytics(a.DB.WithContext(ctx)); err != nil {
		return fmt.Errorf("清理过期书籍分析数据失败: %w", err)
	}
	if err := purgeExpiredTrash(a.DB.WithContext(ctx), now); err != nil {
		return fmt.Errorf("清理过期回收站内容失败: %w", err)
	}
	if err := cleanupExpiredImportSources(now); err != nil {
		return fmt.Errorf("清理过期导入源文件失败: %w", err)
	}
	if err := purgeExpiredRateLimits(a.DB.WithContext(ctx), now); err != nil {
		return fmt.Errorf("清理过期限流计数失败: %w", err)
	}
	if err := a.purgeScheduledAccountDeletions(now); err != nil {
		return fmt.Errorf("清理到期注销账号失败: %w", err)
	}
	if err := a.purgeExpiredAIUsage(ctx, now); err != nil {
		return fmt.Errorf("清理过期 AI 用量记录失败: %w", err)
	}
	if err := a.purgeOldWriterActivity(ctx, now); err != nil {
		return fmt.Errorf("清理过期协作动态失败: %w", err)
	}
	a.purgeOldLogs() // 清理超过留存天数的运行日志文件（失败静默，不阻断维护）
	return nil
}

func (a *App) enqueueMaintenanceIfDue(ctx context.Context, queue *jobqueue.Queue) {
	if queue == nil {
		return
	}
	if _, _, err := queue.EnqueueIfDue(ctx, maintenanceJobType, struct{}{}, 5, maintenanceInterval); err != nil {
		log.Printf("[jobs] enqueue maintenance task failed: %v", err)
	}
}

func (a *App) enqueueSitemapIfDue(ctx context.Context, queue *jobqueue.Queue) {
	if queue == nil {
		return
	}
	if _, _, err := queue.EnqueueIfDue(ctx, sitemapJobType, struct{}{}, 3, sitemapInterval); err != nil {
		log.Printf("[jobs] enqueue sitemap task failed: %v", err)
	}
}

func cleanupExpiredImportSources(now time.Time) error {
	dir := filepath.Join(config.DataDir(), "import-jobs")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := now.Add(-importSourceRetention)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || (!strings.HasPrefix(name, "pdf-") && !strings.HasPrefix(name, "zip-")) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
			continue
		}
		if removeErr := os.Remove(filepath.Join(dir, name)); removeErr != nil && !os.IsNotExist(removeErr) {
			return removeErr
		}
	}
	return nil
}

func (a *App) runZIPImportJob(_ context.Context, raw json.RawMessage) (any, error) {
	var job zipImportJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil, fmt.Errorf("解析 ZIP 导入任务失败: %w", err)
	}
	if job.UserID == 0 || job.Size <= 0 || strings.TrimSpace(job.SourcePath) == "" || strings.TrimSpace(job.Filename) == "" {
		return nil, fmt.Errorf("ZIP 导入任务缺少必要字段")
	}
	source, err := validateImportJobSource(job.SourcePath)
	if err != nil {
		return nil, err
	}
	var user models.User
	if err := a.DB.First(&user, job.UserID).Error; err != nil {
		return nil, fmt.Errorf("导入用户不存在")
	}
	result, _, err := a.importBookFromZIP(storedZIP{Filename: job.Filename, Path: source, Size: job.Size}, &user, job.Title)
	if err != nil {
		return nil, err
	}
	if err := os.Remove(source); err != nil && !os.IsNotExist(err) {
		log.Printf("[jobs] cleanup ZIP import source failed: %v", err)
	}
	return result, nil
}

func validateImportJobSource(sourcePath string) (string, error) {
	root, err := filepath.Abs(filepath.Join(config.DataDir(), "import-jobs"))
	if err != nil {
		return "", fmt.Errorf("解析导入目录失败: %w", err)
	}
	source, err := filepath.Abs(sourcePath)
	if err != nil {
		return "", fmt.Errorf("解析导入源文件路径失败: %w", err)
	}
	relative, err := filepath.Rel(root, source)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("导入源文件路径无效")
	}
	return source, nil
}

func (a *App) runPDFImportJob(_ context.Context, raw json.RawMessage) (any, error) {
	var job pdfImportJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil, fmt.Errorf("解析 PDF 导入任务失败: %w", err)
	}
	if job.UserID == 0 || strings.TrimSpace(job.SourcePath) == "" || strings.TrimSpace(job.Filename) == "" {
		return nil, fmt.Errorf("PDF 导入任务缺少必要字段")
	}
	source, err := validateImportJobSource(job.SourcePath)
	if err != nil {
		return nil, err
	}
	var user models.User
	if err := a.DB.First(&user, job.UserID).Error; err != nil {
		return nil, fmt.Errorf("导入用户不存在")
	}
	upload, err := a.extractStoredPDF(storedPDF{Filename: job.Filename, Path: source})
	if err != nil {
		return nil, err
	}

	var result pdfImportResult
	if job.BookID == 0 {
		title := strings.TrimSpace(strings.TrimSuffix(job.Filename, filepath.Ext(job.Filename)))
		if strings.TrimSpace(job.Title) != "" {
			title = job.Title
		}
		title = truncateText(title, 255)
		chapters := splitPDFChapters(upload.Result.Markdown, title)
		book, createErr := a.createContentImportBook(&user, title, fmt.Sprintf("从 PDF 导入，共 %d 页。", upload.Result.Pages), chapters)
		if createErr != nil {
			return nil, fmt.Errorf("创建 PDF 书籍失败: %w", createErr)
		}
		result = pdfImportResult{Book: &book, BookID: book.ID, ImportedDoc: len(chapters), Pages: upload.Result.Pages, Source: "pdf", Message: fmt.Sprintf("导入完成：《%s》共 %d 个章节", book.Title, len(chapters))}
	} else {
		var book models.Book
		if err := a.DB.First(&book, job.BookID).Error; err != nil {
			return nil, fmt.Errorf("待重新导入的书籍不存在")
		}
		if !a.canManageBook(&user, &book) {
			return nil, fmt.Errorf("已无权重新导入这本书")
		}
		mode := strings.ToLower(strings.TrimSpace(job.Mode))
		if mode != "append" && mode != "replace" {
			return nil, fmt.Errorf("PDF 重新导入方式无效")
		}
		result, err = a.applyPDFReimport(&book, &user, upload, mode)
		if err != nil {
			return nil, fmt.Errorf("重新导入 PDF 失败: %w", err)
		}
	}
	if err := os.Remove(source); err != nil && !os.IsNotExist(err) {
		// 内容已经成功入库，清理失败不能触发任务重试，否则追加模式会重复写入章节。
		// import-jobs 目录中的遗留源文件由后续定期清理兜底。
		log.Printf("[jobs] cleanup PDF import source failed: %v", err)
	}
	return result, nil
}

func (a *App) jobQueue() *jobqueue.Queue {
	a.jobsMu.RLock()
	defer a.jobsMu.RUnlock()
	return a.Jobs
}

// startJobSupervisor 同时覆盖“启动时已安装”和“安装向导在当前进程内完成”两种情况。
// 队列实例发生变化时停止旧 worker，再启动新 worker。
func (a *App) startJobSupervisor(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var active *jobqueue.Queue
	var cancel context.CancelFunc
	nextMaintenanceCheck := time.Time{}
	nextBackupCheck := time.Time{} // 自动备份按计划检查（每 10 分钟）
	for {
		queue := a.jobQueue()
		if queue != nil && queue != active {
			if cancel != nil {
				cancel()
			}
			workerCtx, workerCancel := context.WithCancel(ctx)
			cancel = workerCancel
			active = queue
			// 多实例：登记本实例（心跳、跨实例消息与租约）
			if err := cluster.Start(ctx, a.DB, Version); err != nil {
				log.Printf("[cluster] 实例登记失败: %v", err)
			} else {
				clusterApp.Store(a)
				writeClusterMarker()
				a.syncLocalPluginFiles()
			}
			go queue.Start(workerCtx)
			nextMaintenanceCheck = a.runMaintenance(ctx, queue)
		} else if queue != nil && !currentTime().Before(nextMaintenanceCheck) {
			nextMaintenanceCheck = a.runMaintenance(ctx, queue)
		}
		if queue != nil && !currentTime().Before(nextBackupCheck) {
			a.enqueueBackupIfDue(ctx)
			nextBackupCheck = currentTime().Add(backupCheckInterval)
		}
		select {
		case <-ctx.Done():
			if cancel != nil {
				cancel()
			}
			removeClusterMarker()
			cluster.Stop()
			return
		case <-ticker.C:
		}
	}
}

// runMaintenance 周期维护与插件巡检（含把中断的后台任务标记为可继续）：多实例时只由持有「维护」租约的一个实例执行，
// 持有者按周期续期；未取得租约的实例每分钟再试，持有者下线后很快由其他实例接手。返回下次检查的时间。
func (a *App) runMaintenance(ctx context.Context, queue *jobqueue.Queue) time.Time {
	if !cluster.TryLease("maintenance", maintenanceCheckEvery+30*time.Minute) {
		return currentTime().Add(time.Minute)
	}
	a.enqueueMaintenanceIfDue(ctx, queue)
	a.enqueueSitemapIfDue(ctx, queue)
	a.enqueueNotificationBackfillIfNeeded(ctx, queue)
	plugincore.FireJobQueueSweep(a, queue)
	return currentTime().Add(maintenanceCheckEvery)
}

// enqueueEmail 在生产应用中写入持久化队列。测试或安装后尚未重启的极短窗口内
// 若队列未初始化，则回退到同步发送，保证邮件能力不会静默丢失。
func (a *App) enqueueEmail(ctx context.Context, to, subject, html string) error {
	queue := a.jobQueue()
	if queue == nil {
		return a.mailSender().Send(to, subject, html)
	}
	_, err := queue.Enqueue(ctx, emailSendJobType, emailSendJob{To: to, Subject: subject, HTML: html}, 5)
	return err
}
