package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// 特性插件（成就）：禁用时后台接口 404，启用后 200；启用/禁用切换即切换站点配置开关。
func TestFeaturePluginGate(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("创建应用失败: %v", err)
	}
	ts := httptest.NewServer(a.Router())
	defer ts.Close()

	install := `{"database":` + testdb.InstallJSON(t) + `,"site":{"name":"t"},"admin":{"username":"admin","email":"a@b.c","password":"secret123"}}`
	r, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/setup/install", bytes.NewBufferString(install))
	r.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(r)
	var ip map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&ip)
	resp.Body.Close()
	token := ip["data"].(map[string]any)["token"].(string)

	do := func(method, path string) int {
		req, _ := http.NewRequest(method, ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("请求失败: %v", err)
		}
		defer res.Body.Close()
		return res.StatusCode
	}

	// 默认禁用：成就管理接口 404，且成就表未建（首次启用才建表）
	if st := do(http.MethodGet, "/api/v1/admin/achievement-metrics"); st != http.StatusNotFound {
		t.Fatalf("成就插件禁用时应 404，实际 %d", st)
	}
	if a.DB.Migrator().HasTable("achievement_definitions") {
		t.Fatalf("插件未启用时不应建成就表")
	}
	// 启用插件 → 接口 200，建表，站点开关置真，动态权限注册
	if st := do(http.MethodPost, "/api/v1/admin/plugins/"+pluginAchievements+"/install"); st != http.StatusOK {
		t.Fatalf("启用成就插件应 200，实际 %d", st)
	}
	if !a.pluginEnabled(pluginAchievements) || a.getSetting("achievements_enabled") != "true" {
		t.Fatalf("启用后开关未置真")
	}
	if !a.DB.Migrator().HasTable("achievement_definitions") {
		t.Fatalf("启用后应已建成就表")
	}
	if !authz.Has("admin", authz.AchievementManage) {
		t.Fatalf("启用后管理员应获得成就权限（动态注册）")
	}
	if st := do(http.MethodGet, "/api/v1/admin/achievement-metrics"); st != http.StatusOK {
		t.Fatalf("启用后成就管理应 200，实际 %d", st)
	}
	// 禁用插件 → 接口重新 404
	if st := do(http.MethodPost, "/api/v1/admin/plugins/"+pluginAchievements+"/uninstall"); st != http.StatusOK {
		t.Fatalf("禁用成就插件应 200，实际 %d", st)
	}
	if a.pluginEnabled(pluginAchievements) {
		t.Fatalf("禁用后仍判为启用")
	}
	if st := do(http.MethodGet, "/api/v1/admin/achievement-metrics"); st != http.StatusNotFound {
		t.Fatalf("禁用后成就管理应 404，实际 %d", st)
	}
	if authz.Has("admin", authz.AchievementManage) {
		t.Fatalf("禁用后应移除动态成就权限")
	}
	// 禁用后表仍在（未 purge）；purge 卸载后表被删除
	if !a.DB.Migrator().HasTable("achievement_definitions") {
		t.Fatalf("普通禁用不应删除数据表")
	}
	if st := do(http.MethodPost, "/api/v1/admin/plugins/"+pluginAchievements+"/uninstall?purge=true"); st != http.StatusOK {
		t.Fatalf("purge 卸载应 200，实际 %d", st)
	}
	if a.DB.Migrator().HasTable("achievement_definitions") {
		t.Fatalf("purge 后应删除成就表")
	}
}

// 卸载插件必须真正删除，且不被进行中的安装 goroutine 重新写回（代次守卫）。
func TestPluginUninstallGuardsAgainstStaleInstall(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("创建应用失败: %v", err)
	}
	ts := httptest.NewServer(a.Router())
	defer ts.Close()

	install := `{"database":` + testdb.InstallJSON(t) + `,"site":{"name":"t"},"admin":{"username":"admin","email":"a@b.c","password":"secret123"}}`
	r, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/setup/install", bytes.NewBufferString(install))
	r.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(r)
	var ip map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&ip)
	resp.Body.Close()
	token := ip["data"].(map[string]any)["token"].(string)

	count := func() int64 {
		var n int64
		a.DB.Model(&models.Plugin{}).Where(&models.Plugin{Key: pluginPDFExport}).Count(&n)
		return n
	}
	uninstall := func() int {
		ur, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/admin/plugins/"+pluginPDFExport+"/uninstall", nil)
		ur.Header.Set("Authorization", "Bearer "+token)
		ur2, err := http.DefaultClient.Do(ur)
		if err != nil {
			t.Fatalf("卸载请求失败: %v", err)
		}
		defer ur2.Body.Close()
		return ur2.StatusCode
	}

	// 模拟一次“进行中”的安装：拿到代次并落下 downloading 行
	gen := a.plugins.begin(pluginPDFExport)
	p := models.Plugin{Key: pluginPDFExport}
	a.savePluginMeta(&p, map[string]any{"status": "downloading"})
	if count() != 1 {
		t.Fatalf("安装前置：应存在 1 行，实际 %d", count())
	}

	// 卸载
	if st := uninstall(); st != http.StatusOK {
		t.Fatalf("卸载应 200，实际 %d", st)
	}
	if count() != 0 {
		t.Fatalf("卸载后应无插件行，实际 %d", count())
	}

	// 旧安装 goroutine 事后完成并尝试写回：必须被代次守卫丢弃
	if a.plugins.current(pluginPDFExport, gen) {
		t.Fatalf("卸载后旧代次仍被判为当前")
	}
	p.Installed = true
	if a.plugins.current(pluginPDFExport, gen) {
		a.savePluginMeta(&p, map[string]any{"status": "installed"})
	}
	if count() != 0 {
		t.Fatalf("BUG：卸载后被进行中的安装重新写回（rows=%d）", count())
	}

	// 权限：普通用户与匿名不可卸载
	if st := func() int {
		ur, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/admin/plugins/"+pluginPDFExport+"/uninstall", nil)
		ur2, err := http.DefaultClient.Do(ur)
		if err != nil {
			t.Fatalf("匿名卸载请求失败: %v", err)
		}
		defer ur2.Body.Close()
		return ur2.StatusCode
	}(); st != http.StatusUnauthorized {
		t.Fatalf("匿名卸载应 401，实际 %d", st)
	}
}
