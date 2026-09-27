package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// OAuth 模块集成测试：不访问 GitHub 外网，覆盖
// providers 开关 / 管理员配置 / 授权跳转参数 / state 校验 / 绑定列表 / 解绑规则与权限
func TestOAuthFlow(t *testing.T) {
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

	// 不跟随 302，便于断言 Location
	client := &http.Client{Timeout: 10 * 1e9, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	request := func(method, path string, body any, token string) (int, map[string]any, http.Header) {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return resp.StatusCode, payload, resp.Header
	}

	// 安装，取得管理员令牌
	_, install, _ := request(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "OAuth 测试站"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	adminToken := install["data"].(map[string]any)["token"].(string)

	// 1. 未配置时 providers 标记 github 未启用
	status, providers, _ := request(http.MethodGet, "/api/v1/auth/oauth/providers", nil, "")
	if status != 200 {
		t.Fatalf("providers 应公开可访问: %d", status)
	}
	github := providers["data"].(map[string]any)["providers"].([]any)[0].(map[string]any)
	if github["enabled"] != false {
		t.Fatalf("未配置时 github 应为未启用: %v", github)
	}

	// 2. 未配置时发起跳转 → 回到登录页带 not_configured
	status, _, header := request(http.MethodGet, "/api/v1/auth/oauth/github?origin=http://localhost:3002", nil, "")
	if status != 302 || !strings.Contains(header.Get("Location"), "oauth_error=not_configured") {
		t.Fatalf("未配置发起登录应回跳登录页: %d %s", status, header.Get("Location"))
	}

	// 3. 管理员保存配置；非管理员不可访问
	status, _, _ = request(http.MethodGet, "/api/v1/oauth", nil, "")
	if status != 401 {
		t.Fatalf("未登录读取 OAuth 配置应 401: %d", status)
	}
	status, _, _ = request(http.MethodPut, "/api/v1/oauth", map[string]any{
		"provider": "github", "client_id": "test-client-id", "client_secret": "test-secret", "enabled": true,
	}, adminToken)
	if status != 200 {
		t.Fatalf("保存 OAuth 配置失败: %d", status)
	}
	status, saved, _ := request(http.MethodGet, "/api/v1/oauth", nil, adminToken)
	if status != 200 {
		t.Fatalf("读取 OAuth 配置失败: %d", status)
	}
	savedProviders := saved["data"].(map[string]any)["providers"].([]any)
	if savedProviders[0].(map[string]any)["client_id"] != "test-client-id" {
		t.Fatalf("读取 OAuth 配置异常: %v", saved)
	}

	// 4. 配置后 providers 标记启用，发起跳转 302 到 GitHub 授权页并携带 state
	status, providers, _ = request(http.MethodGet, "/api/v1/auth/oauth/providers", nil, "")
	github = providers["data"].(map[string]any)["providers"].([]any)[0].(map[string]any)
	if github["enabled"] != true {
		t.Fatalf("配置后 github 应为启用: %v", github)
	}
	status, _, header = request(http.MethodGet, "/api/v1/auth/oauth/github?origin=http://localhost:3002", nil, "")
	location := header.Get("Location")
	if status != 302 || !strings.HasPrefix(location, "https://github.com/login/oauth/authorize") ||
		!strings.Contains(location, "client_id=test-client-id") || !strings.Contains(location, "state=") {
		t.Fatalf("发起登录应 302 到 GitHub 授权页: %d %s", status, location)
	}

	// 5. 回调 state 校验：伪造 state 拒绝
	status, _, header = request(http.MethodGet, "/api/v1/auth/oauth/github/callback?code=x&state=bogus", nil, "")
	if status != 302 || !strings.Contains(header.Get("Location"), "oauth_error=invalid_state") {
		t.Fatalf("伪造 state 应被拒绝: %d %s", status, header.Get("Location"))
	}
	// 不支持的 provider（facebook 未接入）
	status, _, header = request(http.MethodGet, "/api/v1/auth/oauth/facebook?origin=http://localhost:3002", nil, "")
	if status != 302 || !strings.Contains(header.Get("Location"), "oauth_error=unsupported_provider") {
		t.Fatalf("不支持的 provider 应回跳错误: %d %s", status, header.Get("Location"))
	}

	// 6. 绑定列表：登录后为空数组
	status, bindings, _ := request(http.MethodGet, "/api/v1/auth/oauth/bindings", nil, adminToken)
	if status != 200 {
		t.Fatalf("绑定列表应可访问: %d", status)
	}
	if _, ok := bindings["data"].(map[string]any)["bindings"].([]any); !ok {
		t.Errorf("bindings 应为 [] 而非 %v", bindings["data"].(map[string]any)["bindings"])
	}

	// 7. 未绑定时解绑 → 404
	status, _, _ = request(http.MethodDelete, "/api/v1/auth/oauth/github", nil, adminToken)
	if status != 404 {
		t.Fatalf("未绑定时解绑应 404: %d", status)
	}

	// 8. 解绑需登录
	status, _, _ = request(http.MethodDelete, "/api/v1/auth/oauth/github", nil, "")
	if status != 401 {
		t.Fatalf("未登录解绑应 401: %d", status)
	}
}

// TestOAuthStateDBBacked 覆盖入库 state 的一次性与失效语义（多实例：不依赖进程内存）。
func TestOAuthStateDBBacked(t *testing.T) {
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
	ir, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/setup/install", strings.NewReader(install))
	ir.Header.Set("Content-Type", "application/json")
	if resp, err := http.DefaultClient.Do(ir); err != nil {
		t.Fatalf("安装失败: %v", err)
	} else {
		resp.Body.Close()
	}

	origin := "https://app.example.com"
	state := a.oauthStateSave(origin, 0)
	if state == "" {
		t.Fatalf("应返回非空 state")
	}

	// 首次取用成功并返回来源
	got, _, ok := a.oauthStateTake(state)
	if !ok || got != origin {
		t.Fatalf("首次取用应成功且来源匹配，got=%q ok=%v", got, ok)
	}
	// 一次性：再次取用失败
	if _, _, ok := a.oauthStateTake(state); ok {
		t.Fatalf("state 应一次性，二次取用须失败")
	}
	// 未知 state 失败
	if _, _, ok := a.oauthStateTake("deadbeef"); ok {
		t.Fatalf("未知 state 应失败")
	}
	// 空 state 失败
	if _, _, ok := a.oauthStateTake(""); ok {
		t.Fatalf("空 state 应失败")
	}
	// 绑定模式：userID 随 state 往返
	linkState := a.oauthStateSave(origin, 42)
	if _, uid, ok := a.oauthStateTake(linkState); !ok || uid != 42 {
		t.Fatalf("绑定模式应带回 userID=42，实际 %d ok=%v", uid, ok)
	}
	// 明文不落库：只存哈希
	var row models.OAuthState
	a.oauthStateSave(origin, 0) // 再存一条用于检查
	if err := a.DB.Last(&row).Error; err != nil {
		t.Fatalf("查询 state 行失败: %v", err)
	}
	if row.StateHash == "" || len(row.StateHash) != 64 {
		t.Fatalf("应存 sha256 十六进制哈希，实际 %q", row.StateHash)
	}
}
