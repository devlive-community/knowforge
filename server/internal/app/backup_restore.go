package app

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"knowforge/server/internal/config"
	"knowforge/server/internal/database"
	"knowforge/server/internal/eventhub"
	"knowforge/server/internal/models"
)

// —— 从备份恢复（安装向导）：在未安装的新实例上，选择数据库（须为空库，可与备份时的数据库类型不同）并上传备份文件，
// 校验格式与版本后建表、写入全部数据、解压本地上传文件，写入配置（沿用备份中的站点密钥，原有登录状态与后台任务数据保持有效），
// 完成后即为已安装状态，用备份中的账号登录。进度以事件流推送；开始写入后即使浏览器断开也会继续完成。——

var restoreMu sync.Mutex

// SetupRestore POST /setup/restore（multipart：file 备份文件，database 数据库配置 JSON）
func (a *App) SetupRestore(c *gin.Context) {
	if a.Config.Installed {
		fail(c, http.StatusForbidden, "系统已安装，只能在全新安装时从备份恢复")
		return
	}
	if !restoreMu.TryLock() {
		fail(c, http.StatusConflict, "正在恢复中，请稍候")
		return
	}
	defer restoreMu.Unlock()

	var dbCfg config.DatabaseConfig
	if err := json.Unmarshal([]byte(c.PostForm("database")), &dbCfg); err != nil {
		fail(c, http.StatusBadRequest, "数据库配置无效")
		return
	}
	if err := normalizeDBConfig(&dbCfg); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	file, err := c.FormFile("file")
	if err != nil {
		fail(c, http.StatusBadRequest, "请选择备份文件")
		return
	}
	sqlDB, err := database.Test(dbCfg)
	if err != nil {
		fail(c, http.StatusBadRequest, "数据库连接失败: "+err.Error())
		return
	}
	_ = sqlDB.Close()

	dir := filepath.Join(config.DataDir(), "restore")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(c, http.StatusInternalServerError, "创建临时目录失败: "+err.Error())
		return
	}
	nonce := make([]byte, 6)
	_, _ = rand.Read(nonce)
	archive := filepath.Join(dir, "restore-"+hex.EncodeToString(nonce)+".zip")
	if err := c.SaveUploadedFile(file, archive); err != nil {
		fail(c, http.StatusInternalServerError, "保存备份文件失败: "+err.Error())
		return
	}
	defer os.Remove(archive)
	zr, manifest, err := readBackupManifest(archive)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	defer zr.Close()

	db, err := database.Open(dbCfg)
	if err != nil {
		fail(c, http.StatusInternalServerError, "打开数据库失败: "+err.Error())
		return
	}
	if db.Migrator().HasTable(&models.User{}) {
		var users int64
		db.Model(&models.User{}).Count(&users)
		if users > 0 {
			fail(c, http.StatusBadRequest, "目标数据库中已有数据，请选择一个空数据库")
			return
		}
	}

	// 以下开始写入：改为事件流推送进度；写入不随请求取消（浏览器断开也继续完成，避免留下一半的数据）
	eventhub.StartSSE(c)
	write := func(name string, v any) {
		raw, _ := json.Marshal(v)
		eventhub.Write(c.Writer, name, raw)
	}
	write("start", gin.H{"app_version": manifest.AppVersion, "created_at": manifest.CreatedAt, "site_name": manifest.SiteName,
		"db_type": manifest.DBType, "tables": len(manifest.Tables), "files": manifest.Files})
	progress := func(stage string, done, total int) {
		write("progress", gin.H{"stage": stage, "done": done, "total": total})
	}
	stats, err := a.restoreFromArchive(context.Background(), db, dbCfg, zr, manifest, progress)
	if err != nil {
		log.Printf("[restore] 从备份恢复失败: %v", err)
		write("error", gin.H{"message": err.Error()})
		return
	}
	write("done", stats)
}

// restoreFromArchive 建表 → 写入数据 → 解压文件 → 写入配置并切换为已安装状态（与启动时已安装的初始化一致）。
func (a *App) restoreFromArchive(ctx context.Context, db *gorm.DB, dbCfg config.DatabaseConfig, zr *zip.ReadCloser, m *backupManifest, progress backupProgress) (restoreStats, error) {
	progress("migrate", 0, 1)
	if err := models.All(db); err != nil {
		return restoreStats{}, fmt.Errorf("数据表迁移失败: %w", err)
	}
	progress("migrate", 1, 1)
	stats, err := restoreBackupDatabase(ctx, db, zr, m, progress)
	if err != nil {
		return stats, err
	}
	if stats.Files, err = restoreBackupFiles(zr, progress); err != nil {
		return stats, fmt.Errorf("恢复上传文件失败: %w", err)
	}
	secret := m.Secret
	if secret == "" {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return stats, fmt.Errorf("生成密钥失败: %w", err)
		}
		secret = hex.EncodeToString(buf)
	}
	a.Config.Installed = true
	a.Config.Secret = secret
	a.Config.InstalledAt = time.Now().Format(time.RFC3339)
	a.Config.Database = dbCfg
	if err := a.Config.Save(); err != nil {
		a.Config.Installed = false
		return stats, fmt.Errorf("保存配置失败: %w", err)
	}
	a.DB = db
	a.syncPluginState()
	a.migrateRegistrationDefaults()
	a.migrateInviteEnabled()
	a.search = configureSearchBackend(db)
	if err := a.configureJobQueue(); err != nil {
		return stats, fmt.Errorf("初始化异步任务失败: %w", err)
	}
	_ = a.setSetting("version", Version, "系统版本")
	return stats, nil
}
