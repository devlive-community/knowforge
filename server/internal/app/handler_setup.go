package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/database"
	"knowforge/server/internal/models"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// Version 服务版本号（构建时可通过 -ldflags "-X ...Version=x.y.z" 覆盖）
var Version = "2026.0.9"

type setupSite struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type setupAdmin struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type installRequest struct {
	Database config.DatabaseConfig `json:"database"`
	Site     setupSite             `json:"site"`
	Admin    setupAdmin            `json:"admin"`
}

// SetupStatus GET /setup/status
func (a *App) SetupStatus(c *gin.Context) {
	resp := gin.H{
		"installed": a.Config.Installed,
		"version":   Version,
		"db_types":  database.SupportedTypes(),
	}
	if a.Config.Installed {
		resp["db_type"] = a.Config.Database.Type
	} else {
		// 安装器需要默认路径；安装完成后不再公开服务器文件系统位置。
		resp["data_dir"] = config.DataDir()
		resp["sqlite_default_path"] = filepath.Join(config.DataDir(), "knowforge.db")
	}
	ok(c, resp)
}

// SetupTest POST /setup/test-connection
func (a *App) SetupTest(c *gin.Context) {
	if a.Config.Installed {
		fail(c, http.StatusNotFound, "接口不存在")
		return
	}
	var req config.DatabaseConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误: "+err.Error())
		return
	}
	if err := normalizeDBConfig(&req); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	sqlDB, err := database.Test(req)
	if err != nil {
		fail(c, http.StatusBadRequest, "数据库连接失败: "+err.Error())
		return
	}
	_ = sqlDB.Close()
	ok(c, gin.H{"message": "数据库连接成功"})
}

// SetupInstall POST /setup/install
func (a *App) SetupInstall(c *gin.Context) {
	if a.Config.Installed {
		fail(c, http.StatusForbidden, "系统已安装，如需重新安装请删除数据目录下的 config.json")
		return
	}

	var req installRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误: "+err.Error())
		return
	}

	if req.Site.Name == "" {
		fail(c, http.StatusBadRequest, "请填写站点名称")
		return
	}
	if !usernameRegex.MatchString(req.Admin.Username) {
		fail(c, http.StatusBadRequest, "管理员用户名需为 3-50 位字母、数字、下划线或中划线")
		return
	}
	if req.Admin.Email != "" && !emailRegex.MatchString(req.Admin.Email) {
		fail(c, http.StatusBadRequest, "管理员邮箱格式不正确")
		return
	}
	if len(req.Admin.Password) < 6 {
		fail(c, http.StatusBadRequest, "管理员密码至少 6 位")
		return
	}
	if err := normalizeDBConfig(&req.Database); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}

	sqlDB, err := database.Test(req.Database)
	if err != nil {
		fail(c, http.StatusBadRequest, "数据库连接失败: "+err.Error())
		return
	}
	_ = sqlDB.Close()

	db, err := database.Open(req.Database)
	if err != nil {
		fail(c, http.StatusInternalServerError, "初始化数据库失败: "+err.Error())
		return
	}
	if err := models.All(db); err != nil {
		fail(c, http.StatusInternalServerError, "数据表迁移失败: "+err.Error())
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Admin.Password), bcrypt.DefaultCost)
	if err != nil {
		fail(c, http.StatusInternalServerError, "密码加密失败")
		return
	}

	admin := models.User{
		Username: req.Admin.Username,
		Email:    req.Admin.Email,
		Password: string(hash),
		Role:     "admin",
		IsActive: true,
		// 安装向导所用的界面语言记为管理员的偏好语言（站点语言刚由迁移种子写入 db）
		PreferredLocale: a.signupLocale(c, db),
	}
	if err := db.Create(&admin).Error; err != nil {
		fail(c, http.StatusInternalServerError, "创建管理员失败: "+err.Error())
		return
	}

	defaultConfigs := []models.SiteConfig{
		{ConfigKey: "site_name", ConfigValue: req.Site.Name, Description: "站点名称"},
		{ConfigKey: "site_description", ConfigValue: req.Site.Description, Description: "站点描述"},
		{ConfigKey: "installation_date", ConfigValue: time.Now().Format(time.RFC3339), Description: "安装日期"},
		{ConfigKey: "version", ConfigValue: Version, Description: "系统版本"},
	}
	if err := db.Create(&defaultConfigs).Error; err != nil {
		fail(c, http.StatusInternalServerError, "写入站点配置失败: "+err.Error())
		return
	}

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		fail(c, http.StatusInternalServerError, "生成密钥失败")
		return
	}

	a.Config.Installed = true
	a.Config.Secret = hex.EncodeToString(secret)
	a.Config.InstalledAt = time.Now().Format(time.RFC3339)
	a.Config.Database = req.Database
	if err := a.Config.Save(); err != nil {
		fail(c, http.StatusInternalServerError, "保存配置失败: "+err.Error())
		return
	}
	a.DB = db
	a.syncPluginState() // 安装完成后为默认启用的插件（如标签）建表并注册权限
	// 限流存储不在此切换：本安装进程沿用内存实现（安装期天然单实例，无多实例竞争），
	// 重启后走 New() 的已安装分支切到数据库共享限流。避免安装期覆盖调用方预设的存储。
	a.search = configureSearchBackend(db)
	if err := a.configureJobQueue(); err != nil {
		fail(c, http.StatusInternalServerError, "初始化异步任务失败: "+err.Error())
		return
	}
	token, err := auth.GenerateToken(a.Config.Secret, admin.ID, admin.Username, admin.Role)
	if err != nil {
		fail(c, http.StatusInternalServerError, "签发令牌失败")
		return
	}
	c.SetCookie("knowforge_token", token, 7*24*3600, "/", "", false, false)
	ok(c, gin.H{"token": token, "user": admin})
}

// normalizeDBConfig 校验并补全数据库配置
func normalizeDBConfig(cfg *config.DatabaseConfig) error {
	if !database.Supported(cfg.Type) {
		return fmt.Errorf("不支持的数据库类型: %s", cfg.Type)
	}
	switch cfg.Type {
	case database.TypeSQLite:
		if cfg.Path == "" {
			cfg.Path = filepath.Join(config.DataDir(), "knowforge.db")
		} else if !filepath.IsAbs(cfg.Path) {
			// 相对路径统一解析到数据目录，不受进程工作目录影响
			cfg.Path = filepath.Join(config.DataDir(), cfg.Path)
		}
		if dir := filepath.Dir(cfg.Path); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("数据库目录 %s 无法创建或不可写: %w", dir, err)
			}
			// 实际写探测：systemd 沙箱（ProtectSystem=strict）下即使目录权限为 777 也会写入失败
			probe := filepath.Join(dir, ".knowforge-write-probe")
			if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
				return fmt.Errorf(
					"数据库目录 %s 不可写（服务账户无权限或被部署沙箱限制）。请使用数据目录内的路径，例如 %s",
					dir, filepath.Join(config.DataDir(), "knowforge.db"))
			}
			_ = os.Remove(probe)
		}
	case database.TypeMySQL, database.TypePostgres:
		if cfg.Host == "" {
			return fmt.Errorf("请填写数据库主机地址")
		}
		if cfg.Name == "" {
			return fmt.Errorf("请填写数据库名称")
		}
		if cfg.User == "" {
			return fmt.Errorf("请填写数据库用户名")
		}
	}
	return nil
}
