package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"knowforge/server/internal/config"
	"knowforge/server/internal/testdb"

	"github.com/pquerna/otp/totp"
)

// TestLoginTwoPhaseTwoFactor 覆盖两步登录：第一步（用户名/密码/验证码）通过后下发 login_token，
// 第二步仅凭 login_token + 动态码完成登录，不再校验验证码（修复验证码被消费后二次报错）。
func TestLoginTwoPhaseTwoFactor(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("创建应用失败: %v", err)
	}
	server := httptest.NewServer(a.Router())
	defer server.Close()
	client := &http.Client{Timeout: 10 * time.Second}

	request := func(method, path string, body any, token string) (int, map[string]any) {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		payload := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return resp.StatusCode, payload
	}

	request(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "2FA 登录测试"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	_, reg := request(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "reader", "email": "reader@test.local", "password": "secret123",
	}, "")
	token := reg["data"].(map[string]any)["token"].(string)

	// 开启 2FA（默认勾选含 login）
	_, setup := request(http.MethodPost, "/api/v1/auth/2fa/setup", nil, token)
	secret := setup["data"].(map[string]any)["secret"].(string)
	code, _ := totp.GenerateCode(secret, time.Now())
	status, _ := request(http.MethodPost, "/api/v1/auth/2fa/enable", map[string]any{"code": code}, token)
	if status != http.StatusOK {
		t.Fatalf("开启 2FA 失败: %d", status)
	}

	// 第一步：用户名/密码正确 → 返回 two_factor_required + login_token，不发 token
	status, phase1 := request(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": "reader", "password": "secret123",
	}, "")
	if status != http.StatusOK {
		t.Fatalf("第一步登录应 200: %d", status)
	}
	d1 := phase1["data"].(map[string]any)
	if d1["two_factor_required"] != true {
		t.Fatalf("应要求二次认证: %v", d1)
	}
	loginToken, _ := d1["login_token"].(string)
	if loginToken == "" {
		t.Fatalf("应下发 login_token")
	}
	if _, ok := d1["token"]; ok {
		t.Fatalf("第一步不应直接发登录 token")
	}

	// 第二步：错误动态码 → 401，且挑战未消费（可重试）
	status, _ = request(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"login_token": loginToken, "two_factor_code": "000000",
	}, "")
	if status != http.StatusUnauthorized {
		t.Fatalf("错误动态码应 401: %d", status)
	}

	// 第二步：正确动态码 → 发 token（全程未再提交验证码）
	code2, _ := totp.GenerateCode(secret, time.Now())
	status, phase2 := request(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"login_token": loginToken, "two_factor_code": code2,
	}, "")
	if status != http.StatusOK {
		t.Fatalf("正确动态码应 200: %d %v", status, phase2)
	}
	if _, ok := phase2["data"].(map[string]any)["token"]; !ok {
		t.Fatalf("第二步应发登录 token: %v", phase2)
	}

	// 挑战一次性：再次使用同一 login_token 应失败
	status, _ = request(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"login_token": loginToken, "two_factor_code": code2,
	}, "")
	if status == http.StatusOK {
		t.Fatalf("login_token 应一次性，复用须失败")
	}
}
