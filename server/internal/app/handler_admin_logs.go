package app

import (
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// AdminGetLogConfig GET /admin/logs 读取运行日志配置与当前日志文件列表。
func (a *App) AdminGetLogConfig(c *gin.Context) {
	type logFile struct {
		Name     string `json:"name"`
		Size     int64  `json:"size"`
		Modified string `json:"modified"`
	}
	files := []logFile{}
	if entries, err := os.ReadDir(a.logDir()); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasPrefix(e.Name(), "knowforge-") || !strings.HasSuffix(e.Name(), ".log") {
				continue
			}
			info, ierr := e.Info()
			if ierr != nil {
				continue
			}
			files = append(files, logFile{Name: e.Name(), Size: info.Size(), Modified: info.ModTime().Format("2006-01-02 15:04")})
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Name > files[j].Name }) // 最新在前
	}
	level := strings.ToLower(strings.TrimSpace(a.getSetting("log_level")))
	if level == "" {
		level = "info"
	}
	ok(c, gin.H{
		"enabled":        a.getSetting("log_enabled") != "false",
		"dir":            a.logDir(),
		"level":          level,
		"retention_days": a.logRetentionDays(),
		"files":          files,
	})
}

// AdminUpdateLogConfig PUT /admin/logs 更新运行日志配置并即时生效（重建 writer）。
func (a *App) AdminUpdateLogConfig(c *gin.Context) {
	var req struct {
		Enabled       *bool   `json:"enabled"`
		Dir           *string `json:"dir"`
		Level         *string `json:"level"`
		RetentionDays *int    `json:"retention_days"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if req.Enabled != nil {
		val := "true"
		if !*req.Enabled {
			val = "false"
		}
		_ = a.setSetting("log_enabled", val, "运行日志启用")
	}
	if req.Dir != nil {
		_ = a.setSetting("log_dir", strings.TrimSpace(*req.Dir), "运行日志目录")
	}
	if req.Level != nil {
		lv := strings.ToLower(strings.TrimSpace(*req.Level))
		switch lv {
		case "debug", "info", "warn", "error":
		default:
			fail(c, http.StatusBadRequest, "日志等级仅支持 debug/info/warn/error")
			return
		}
		_ = a.setSetting("log_level", lv, "运行日志等级")
	}
	if req.RetentionDays != nil {
		n := *req.RetentionDays
		if n < 1 || n > 3650 {
			fail(c, http.StatusBadRequest, "留存天数需在 1–3650 之间")
			return
		}
		_ = a.setSetting("log_retention_days", strconv.Itoa(n), "运行日志留存天数")
	}
	a.initLogging() // 立即按新配置重建文件 writer
	broadcastReload(reloadLogging)
	a.Infof("运行日志配置已更新")
	a.AdminGetLogConfig(c)
}
