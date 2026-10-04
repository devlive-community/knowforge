package app

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/config"
	"knowforge/server/internal/eventhub"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"

	"github.com/gin-gonic/gin"
)

// 插件系统：目前仅内置「PDF 导出」插件，安装时下载官方 chrome-headless-shell 到数据目录，
// 保持基础二进制/镜像轻量；未安装则 PDF 导出不可用。

// 插件键与类型的单一事实来源在 internal/plugins 包；app 内的这些常量只是别名，方便现有代码引用。
const (
	pluginPDFExport        = plugins.KeyPDFExport
	pluginAchievements     = plugins.KeyAchievements
	pluginTags             = plugins.KeyTags
	pluginBookTranslations = plugins.KeyBookTranslations
	pluginBookVersions     = plugins.KeyBookVersions
	pluginBookFollow       = plugins.KeyBookFollow
	pluginGrowth           = plugins.KeyGrowth
	pluginContentCollect   = plugins.KeyContentCollect
	pluginWatermark        = plugins.KeyWatermark
	// pluginKindRuntime 需要下载运行时依赖（二进制/镜像）的插件；pluginKindFeature 仅切换某项功能的启用/禁用。
	pluginKindRuntime = plugins.KindRuntime
	pluginKindFeature = plugins.KindFeature
)

// pluginInfo 声明式元数据（plugins.Meta，来自各插件子包 internal/plugins/<name>/）。
// 启用后的初始化行为由子包经 plugincore.OnPluginEnabled 登记。
type pluginInfo struct {
	plugins.Meta
}

// pluginRegistry 由各插件子包自注册的元数据构建（禁止在此硬编码「有哪些插件」）。
// 子包在 blank import（plugins_import.go）触发的 init() 中调用 plugins.Register，
// 本包 init() 再据 plugins.All() 组装。
var pluginRegistry []pluginInfo

func init() {
	for _, m := range plugins.All() {
		pluginRegistry = append(pluginRegistry, pluginInfo{Meta: m})
	}
}

// pluginEnabled 判定插件是否启用：feature 插件优先看其复用的站点配置开关（无则看 Plugin.Installed，内置默认启用）；
// runtime 插件看运行时依赖是否已安装。禁用即前后端全禁的唯一判定入口。
func (a *App) pluginEnabled(key string) bool {
	info := pluginInfoByKey(key)
	if info == nil {
		return false
	}
	if info.Kind == pluginKindFeature {
		if info.EnabledKey != "" {
			return a.getSetting(info.EnabledKey) == "true"
		}
		var p models.Plugin
		if err := a.DB.Where(&models.Plugin{Key: key}).First(&p).Error; err != nil {
			return true // 内置特性插件默认启用
		}
		return p.Installed
	}
	var p models.Plugin
	return a.DB.Where(&models.Plugin{Key: key, Installed: true}).First(&p).Error == nil
}

// RequireFeaturePlugin 特性插件启用守卫：插件被禁用时对应后端接口直接 404，确保「禁用即前后端全禁」。
func (a *App) RequireFeaturePlugin(key string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !a.pluginEnabled(key) {
			fail(c, http.StatusNotFound, "功能未启用")
			c.Abort()
			return
		}
		c.Next()
	}
}

// setFeaturePluginEnabled 切换 feature 插件启用状态：优先写其复用的站点配置开关，否则以 Plugin.Installed 记录。
func (a *App) setFeaturePluginEnabled(info *pluginInfo, enabled bool) error {
	if info.EnabledKey != "" {
		return a.setSetting(info.EnabledKey, strconv.FormatBool(enabled), info.Name+" 启用开关")
	}
	var p models.Plugin
	if err := a.DB.Where(&models.Plugin{Key: info.Key}).First(&p).Error; err != nil {
		p = models.Plugin{Key: info.Key}
	}
	p.Installed = enabled
	if enabled {
		now := time.Now()
		p.InstalledAt = &now
	}
	status := "disabled"
	if enabled {
		status = "enabled"
	}
	a.savePluginMeta(&p, map[string]any{"status": status})
	return nil
}

// migratePluginModels 为插件建表（幂等；仅在启用时调用，实现「首次启用才建表」）。
func (a *App) migratePluginModels(info *pluginInfo) error {
	if len(info.Models) == 0 {
		return nil
	}
	return a.DB.AutoMigrate(info.Models...)
}

// syncPluginPermissions 用当前启用的 feature 插件重算 authz 动态权限覆盖层（禁用插件即移除其权限）。
func (a *App) syncPluginPermissions() {
	admin := []authz.Permission{}
	user := []authz.Permission{}
	for i := range pluginRegistry {
		info := &pluginRegistry[i]
		if info.Kind != pluginKindFeature || !a.pluginEnabled(info.Key) {
			continue
		}
		admin = append(admin, info.AdminPerms...)
		user = append(user, info.UserPerms...)
	}
	authz.SetPluginPermissions(admin, user)
}

// syncPluginState 启动时调用：为已启用的 feature 插件建表，并重算动态权限。
func (a *App) syncPluginState() {
	for i := range pluginRegistry {
		info := &pluginRegistry[i]
		if info.Kind == pluginKindFeature && a.pluginEnabled(info.Key) {
			if err := a.migratePluginModels(info); err != nil {
				log.Printf("plugin %s migrate failed: %v", info.Key, err)
			}
		}
	}
	a.syncPluginPermissions()
}

func pluginInfoByKey(key string) *pluginInfo {
	for i := range pluginRegistry {
		if pluginRegistry[i].Key == key {
			return &pluginRegistry[i]
		}
	}
	return nil
}

// pluginDir 插件的数据目录
func pluginDir(key string) string {
	return filepath.Join(config.DataDir(), "plugins", key)
}

// pluginMeta 解析 Plugin.Meta JSON
func pluginMeta(p *models.Plugin) map[string]any {
	m := map[string]any{}
	if p.Meta != "" {
		_ = json.Unmarshal([]byte(p.Meta), &m)
	}
	return m
}

func (a *App) savePluginMeta(p *models.Plugin, meta map[string]any) {
	raw, _ := json.Marshal(meta)
	p.Meta = string(raw)
	a.DB.Save(p)
}

// cftPlatform 将 Go 平台映射到 Chrome for Testing 平台标识
func cftPlatform() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		return "linux64", nil
	case "darwin/amd64":
		return "mac-x64", nil
	case "darwin/arm64":
		return "mac-arm64", nil
	case "windows/amd64":
		return "win64", nil
	default:
		return "", fmt.Errorf("不支持的平台 %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}

// resolveChromeDownload 从 Chrome for Testing 稳定通道解析当前平台的 chrome-headless-shell 下载地址
func resolveChromeDownload() (version, url string, err error) {
	platform, err := cftPlatform()
	if err != nil {
		return "", "", err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get("https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json")
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	var payload struct {
		Channels map[string]struct {
			Version   string `json:"version"`
			Downloads struct {
				ChromeHeadlessShell []struct {
					Platform string `json:"platform"`
					URL      string `json:"url"`
				} `json:"chrome-headless-shell"`
			} `json:"downloads"`
		} `json:"channels"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", "", err
	}
	stable, ok := payload.Channels["Stable"]
	if !ok {
		return "", "", fmt.Errorf("未找到稳定通道版本")
	}
	for _, d := range stable.Downloads.ChromeHeadlessShell {
		if d.Platform == platform {
			return stable.Version, d.URL, nil
		}
	}
	return "", "", fmt.Errorf("稳定通道未提供 %s 平台的 chrome-headless-shell", platform)
}

// chromeBinaryName 平台对应的可执行文件名
func chromeBinaryName() string {
	if runtime.GOOS == "windows" {
		return "chrome-headless-shell.exe"
	}
	return "chrome-headless-shell"
}

// installedChromePath 返回已安装插件记录的 chrome 可执行路径（未安装返回空）
func (a *App) installedChromePath() string {
	var p models.Plugin
	if err := a.DB.Where(&models.Plugin{Key: pluginPDFExport, Installed: true}).First(&p).Error; err != nil {
		return ""
	}
	path := a.localChromePath(&p)
	if path == "" {
		a.syncLocalPluginFiles() // 其他实例安装的：本实例补下载
	}
	return path
}

// runPluginInstall 后台下载并解压 chrome-headless-shell，完成后更新插件记录。
// gen 为本次操作的代次令牌：若期间被卸载/重装（代次变化），所有 DB 写入都会被丢弃，
// 避免旧 goroutine 把已删除的插件行重新写回。
func (a *App) runPluginInstall(p *models.Plugin, gen int64) {
	key := p.Key
	// save 仅在本代次仍有效时写库，否则丢弃（插件已被卸载/重装）
	save := func(meta map[string]any) {
		if !a.plugins.current(key, gen) {
			return
		}
		a.savePluginMeta(p, meta)
	}
	logf := func(level, text string) { a.plugins.log(key, level, text) }
	fail := func(reason string) {
		logf("error", reason)
		save(map[string]any{"status": "failed", "error": reason})
	}

	version, binPath, err := fetchChrome(logf)
	if err != nil {
		fail(err.Error())
		return
	}
	dir := pluginDir(pluginPDFExport)

	// 若已被卸载则不落库，并清理刚下载的文件
	if !a.plugins.current(key, gen) {
		logf("error", "安装已被取消，清理下载文件")
		_ = os.RemoveAll(dir)
		return
	}
	now := time.Now()
	p.Installed = true
	p.Version = version
	p.InstalledAt = &now
	save(map[string]any{"status": "installed", "chrome_path": binPath})
	logf("success", "安装完成：版本 "+version)
	broadcastReload(reloadPlugins) // 其他实例补齐本地文件
}

// fetchChrome 下载、解压并自检 chrome-headless-shell 到本实例的插件目录，返回版本与可执行文件路径。
func fetchChrome(logf func(level, text string)) (string, string, error) {
	logf("info", "开始安装：解析 chrome-headless-shell 下载地址…")
	version, url, err := resolveChromeDownload()
	if err != nil {
		return "", "", fmt.Errorf("解析下载地址失败: %w", err)
	}
	logf("info", "已解析版本 "+version+"，开始下载…")
	dir := pluginDir(pluginPDFExport)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("创建插件目录失败: %w", err)
	}
	zipPath := filepath.Join(dir, "chrome.zip")
	if err := downloadFile(url, zipPath); err != nil {
		return "", "", fmt.Errorf("下载失败: %w", err)
	}
	logf("info", "下载完成，正在解压…")
	chromeRoot := filepath.Join(dir, "chrome")
	if err := unzipTo(zipPath, chromeRoot); err != nil {
		return "", "", fmt.Errorf("解压失败: %w", err)
	}
	_ = os.Remove(zipPath)
	binPath := findChromeBinary(chromeRoot)
	if binPath == "" {
		return "", "", errors.New("未在下载包中找到 chrome-headless-shell 可执行文件")
	}
	_ = os.Chmod(binPath, 0o755)
	logf("info", "正在校验可执行文件…")
	if out, err := exec.Command(binPath, "--version").CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("chrome-headless-shell 自检失败: %s", strings.TrimSpace(string(out)))
	}
	return version, binPath, nil
}

// localChromeFetching 本实例是否正在补下载 chrome（避免重复下载）。
var localChromeFetching atomic.Bool

// syncLocalPluginFiles 多实例：插件在其他实例上安装后，本实例补齐本地文件（如 PDF 导出所需的 chrome），不改数据库记录。
func (a *App) syncLocalPluginFiles() {
	var p models.Plugin
	if a.DB == nil || a.DB.Where(&models.Plugin{Key: pluginPDFExport, Installed: true}).First(&p).Error != nil || a.localChromePath(&p) != "" {
		return
	}
	if !localChromeFetching.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer localChromeFetching.Store(false)
		if _, _, err := fetchChrome(func(_, text string) { log.Printf("[plugins] 补齐本实例的 PDF 导出组件：%s", text) }); err != nil {
			log.Printf("[plugins] 补齐本实例的 PDF 导出组件失败: %v", err)
		}
	}()
}

// localChromePath 本实例上可用的 chrome 路径：优先插件记录中的路径，其次本实例插件目录中找到的。
func (a *App) localChromePath(p *models.Plugin) string {
	if path, _ := pluginMeta(p)["chrome_path"].(string); path != "" {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return findChromeBinary(filepath.Join(pluginDir(pluginPDFExport), "chrome"))
}

// findChromeBinary 在解压目录中递归查找 chrome-headless-shell 可执行文件
func findChromeBinary(root string) string {
	target := chromeBinaryName()
	var found string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if info.Name() == target {
			found = path
			return io.EOF // 提前结束
		}
		return nil
	})
	return found
}

// unzipTo 解压 zip 到目标目录（保留可执行权限）
func unzipTo(src, dst string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		fp := filepath.Join(dst, f.Name)
		if !strings.HasPrefix(fp, filepath.Clean(dst)+string(os.PathSeparator)) {
			return fmt.Errorf("非法压缩路径: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(fp, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(fp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		out.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// ---------- 管理端点 ----------

// AdminListPlugins GET /admin/plugins 列出插件及安装状态
func (a *App) AdminListPlugins(c *gin.Context) {
	var rows []models.Plugin
	a.DB.Find(&rows)
	state := map[string]models.Plugin{}
	for _, r := range rows {
		state[r.Key] = r
	}
	items := make([]gin.H, 0, len(pluginRegistry))
	for _, info := range pluginRegistry {
		p := state[info.Key]
		items = append(items, gin.H{
			"key":         info.Key,
			"name":        info.Name,
			"description": info.Description,
			"size_hint":   info.SizeHint,
			"kind":        info.Kind,
			"builtin":     info.Builtin,
			"purgeable":   len(info.Tables) > 0,
			"installed":   a.pluginEnabled(info.Key),
			"version":     p.Version,
			"status":      pluginMeta(&p)["status"],
			"error":       pluginMeta(&p)["error"],
		})
	}
	ok(c, gin.H{"items": items})
}

// AdminInstallPlugin POST /admin/plugins/:key/install 触发后台安装
func (a *App) AdminInstallPlugin(c *gin.Context) {
	key := c.Param("key")
	info := pluginInfoByKey(key)
	if info == nil {
		fail(c, http.StatusNotFound, "插件不存在")
		return
	}
	// feature 插件：启用即切换开关，无需下载
	if info.Kind == pluginKindFeature {
		wasEnabled := a.pluginEnabled(key)
		if err := a.setFeaturePluginEnabled(info, true); err != nil {
			fail(c, http.StatusInternalServerError, "启用失败: "+err.Error())
			return
		}
		if !wasEnabled {
			// 首次启用：建表 + 初始化钩子（如成就重算，避免还要去模块设置里再保存一次）
			if err := a.migratePluginModels(info); err != nil {
				fail(c, http.StatusInternalServerError, "建表失败: "+err.Error())
				return
			}
			for _, hook := range plugincore.PluginEnabledHooks(info.Key) { // 插件子包登记的启用钩子（如成就重算、种子等级）
				_ = hook(a)
			}
		}
		a.syncPluginPermissions() // 注册该插件权限
		broadcastReload(reloadPlugins)
		ok(c, gin.H{"message": "已启用", "status": "enabled"})
		return
	}
	var p models.Plugin
	if err := a.DB.Where(&models.Plugin{Key: key}).First(&p).Error; err != nil {
		p = models.Plugin{Key: key}
	}
	if s, _ := pluginMeta(&p)["status"].(string); s == "downloading" {
		fail(c, http.StatusConflict, "插件正在安装中")
		return
	}
	gen := a.plugins.begin(key)
	p.Installed = false
	a.savePluginMeta(&p, map[string]any{"status": "downloading"})
	go a.runPluginInstall(&p, gen)
	ok(c, gin.H{"message": "已开始安装，请稍候刷新状态", "status": "downloading"})
}

// AdminUninstallPlugin POST /admin/plugins/:key/uninstall 卸载并清理下载文件
func (a *App) AdminUninstallPlugin(c *gin.Context) {
	key := c.Param("key")
	info := pluginInfoByKey(key)
	if info == nil {
		fail(c, http.StatusNotFound, "插件不存在")
		return
	}
	// feature 插件：禁用即切换开关，保留记录；?purge=true 额外清除该插件数据表
	if info.Kind == pluginKindFeature {
		if err := a.setFeaturePluginEnabled(info, false); err != nil {
			fail(c, http.StatusInternalServerError, "禁用失败: "+err.Error())
			return
		}
		a.syncPluginPermissions() // 移除该插件权限
		broadcastReload(reloadPlugins)
		purged := false
		if c.Query("purge") == "true" && len(info.Tables) > 0 {
			for _, tbl := range info.Tables {
				if err := a.DB.Migrator().DropTable(tbl); err != nil {
					log.Printf("plugin %s drop table %s failed: %v", key, tbl, err)
				}
			}
			purged = true
		}
		if purged {
			ok(c, gin.H{"message": "已禁用并清除数据"})
		} else {
			ok(c, gin.H{"message": "已禁用"})
		}
		return
	}
	// 递增代次，使任何进行中的安装 goroutine 的后续写入全部失效，避免卸载后被重新写回
	a.plugins.begin(key)
	a.plugins.log(key, "info", "开始卸载：清理下载文件…")
	_ = os.RemoveAll(pluginDir(key))
	if err := a.DB.Where(&models.Plugin{Key: key}).Delete(&models.Plugin{}).Error; err != nil {
		a.plugins.log(key, "error", fmt.Sprintf("卸载失败：删除数据库记录出错：%v", err))
		fail(c, http.StatusInternalServerError, fmt.Sprintf("卸载失败：删除数据库记录出错：%v", err))
		return
	}
	a.plugins.log(key, "success", "已卸载")
	ok(c, gin.H{"message": "已卸载"})
}

// AdminPluginLogs GET /admin/plugins/:key/logs SSE 推送插件安装/卸载日志
func (a *App) AdminPluginLogs(c *gin.Context) {
	key := c.Param("key")
	if pluginInfoByKey(key) == nil {
		fail(c, http.StatusNotFound, "插件不存在")
		return
	}
	// EventSource 无法带请求头，令牌经 query 传入
	// 鉴权：请求头/Cookie 登录态或 ?ticket= 短时事件流凭证（不接受 URL 中的登录令牌）
	user := a.streamUser(c)
	if user == nil || !authz.Has(user.Role, authz.PluginManage) {
		failStream(c)
		return
	}

	eventhub.StartSSE(c)
	flusher, canFlush := c.Writer.(http.Flusher)
	if !canFlush {
		return
	}

	history, ch := a.plugins.subscribe(key)
	defer a.plugins.unsubscribe(key, ch)
	send := func(line pluginLogLine) {
		raw, _ := json.Marshal(line)
		fmt.Fprintf(c.Writer, "data: %s\n\n", raw)
		flusher.Flush()
	}
	for _, line := range history {
		send(line)
	}

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case line := <-ch:
			send(line)
		case <-heartbeat.C:
			fmt.Fprint(c.Writer, ": ping\n\n")
			flusher.Flush()
		}
	}
}
