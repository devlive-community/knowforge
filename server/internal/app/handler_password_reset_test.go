package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"knowforge/server/internal/config"
	"knowforge/server/internal/testdb"
)

type mailRecorder struct {
	sends []string
}

func (r *mailRecorder) Send(to, _, body string) error {
	r.sends = append(r.sends, to+"|"+body)
	return nil
}

// 找回密码集成测试：邮箱存在性不泄露 / 令牌一次性 / 旧令牌作废 / 密码生效 / 管理端配置
func TestPasswordReset(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("创建应用失败: %v", err)
	}
	// 本测试集中验证找回密码的所有业务分支；限流行为由 middleware_rate_limit_test 单独覆盖。
	a.RateLimits = allowAllRateLimitStore{}
	recorder := &mailRecorder{}
	a.MailSender = recorder
	ts := httptest.NewServer(a.Router())
	defer ts.Close()

	client := &http.Client{Timeout: 10 * 1e9}
	request := func(method, path string, body any, token string) (int, map[string]any) {
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
		return resp.StatusCode, payload
	}
	runNextJob := func() {
		t.Helper()
		ran, err := a.Jobs.RunOnce(context.Background())
		if err != nil || !ran {
			t.Fatalf("执行邮件任务失败: ran=%v err=%v", ran, err)
		}
	}

	// 安装 + 注册 alice（携带邮箱）
	_, install := request(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "找回密码测试站"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	adminToken := install["data"].(map[string]any)["token"].(string)
	request(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "alice", "email": "alice@test.local", "password": "secret123",
	}, "")

	// 1. 格式错误 → 400
	status, _ := request(http.MethodPost, "/api/v1/auth/password/forgot", map[string]any{"email": "not-an-email"}, "")
	if status != 400 {
		t.Fatalf("格式错误的邮箱应 400: %d", status)
	}

	// 2. 未注册邮箱 → 200 且不发信（存在性不泄露）
	status, resp := request(http.MethodPost, "/api/v1/auth/password/forgot", map[string]any{"email": "ghost@test.local"}, "")
	if status != 200 {
		t.Fatalf("未注册邮箱应返回 200: %d", status)
	}
	if _, ok := resp["data"].(map[string]any)["message"]; !ok {
		t.Fatalf("未注册邮箱应返回通用提示: %v", resp)
	}
	if len(recorder.sends) != 0 {
		t.Fatalf("未注册邮箱不应发信: %v", recorder.sends)
	}

	// 3. 已注册邮箱（大小写混排）→ 发信并携带重置链接
	status, _ = request(http.MethodPost, "/api/v1/auth/password/forgot", map[string]any{"email": "Alice@Test.Local"}, "")
	if len(recorder.sends) != 0 {
		t.Fatalf("找回密码请求不应同步发送邮件: %v", recorder.sends)
	}
	runNextJob()
	if status != 200 || len(recorder.sends) != 1 {
		t.Fatalf("已注册邮箱应发信: %d %v", status, recorder.sends)
	}
	if !strings.HasPrefix(recorder.sends[0], "alice@test.local|") {
		t.Fatalf("收件人应归一化为小写: %q", recorder.sends[0])
	}
	token := extractResetToken(recorder.sends[0])
	if token == "" {
		t.Fatalf("邮件正文应包含重置链接: %q", recorder.sends[0])
	}

	// 4. 短密码 → 400；随后正确重置 → 200
	status, _ = request(http.MethodPost, "/api/v1/auth/password/reset", map[string]any{"token": token, "password": "123"}, "")
	if status != 400 {
		t.Fatalf("短密码应 400: %d", status)
	}
	status, _ = request(http.MethodPost, "/api/v1/auth/password/reset", map[string]any{"token": token, "password": "newpass456"}, "")
	if status != 200 {
		t.Fatalf("重置密码应 200: %d", status)
	}

	// 5. 旧密码失效、新密码可登录
	status, _ = request(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": "alice", "password": "secret123"}, "")
	if status != 401 {
		t.Fatalf("旧密码应登录失败: %d", status)
	}
	status, _ = request(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": "alice", "password": "newpass456"}, "")
	if status != 200 {
		t.Fatalf("新密码应可登录: %d", status)
	}

	// 6. 令牌一次性：重用已用令牌 → 400
	status, _ = request(http.MethodPost, "/api/v1/auth/password/reset", map[string]any{"token": token, "password": "again789"}, "")
	if status != 400 {
		t.Fatalf("重用令牌应 400: %d", status)
	}

	// 7. 重新申请后旧令牌作废（未用即被删除）
	request(http.MethodPost, "/api/v1/auth/password/forgot", map[string]any{"email": "alice@test.local"}, "")
	runNextJob()
	staleToken := token
	request(http.MethodPost, "/api/v1/auth/password/forgot", map[string]any{"email": "alice@test.local"}, "")
	runNextJob()
	latest := extractResetToken(recorder.sends[len(recorder.sends)-1])
	status, _ = request(http.MethodPost, "/api/v1/auth/password/reset", map[string]any{"token": staleToken, "password": "hijack000"}, "")
	if status != 400 {
		t.Fatalf("被作废的旧令牌应 400: %d", status)
	}
	status, _ = request(http.MethodPost, "/api/v1/auth/password/reset", map[string]any{"token": latest, "password": "final123"}, "")
	if status != 200 {
		t.Fatalf("最新令牌应可重置: %d", status)
	}

	// 8. 管理端邮件配置：未登录 401，读写闭环
	status, _ = request(http.MethodGet, "/api/v1/mail", nil, "")
	if status != 401 {
		t.Fatalf("未登录读取邮件配置应 401: %d", status)
	}
	status, _ = request(http.MethodPut, "/api/v1/mail", map[string]any{
		"driver": "smtp", "host": "smtp.example.com", "port": 465,
		"username": "noreply", "password": "smtp-secret", "from": "noreply@example.com",
		"site_url": "https://knowforge.example.com",
	}, adminToken)
	if status != 200 {
		t.Fatalf("保存邮件配置失败: %d", status)
	}
	status, got := request(http.MethodGet, "/api/v1/mail", nil, adminToken)
	data := got["data"].(map[string]any)
	if status != 200 || data["driver"] != "smtp" || data["host"] != "smtp.example.com" ||
		data["site_url"] != "https://knowforge.example.com" {
		t.Fatalf("邮件配置读取异常: %d %v", status, data)
	}
	status, _ = request(http.MethodPut, "/api/v1/mail", map[string]any{"driver": "sendgrid"}, adminToken)
	if status != 400 {
		t.Fatalf("非法驱动应 400: %d", status)
	}

	// 9. site_url 配置后邮件链接使用站点地址
	// 先排空此前累积的后台任务（site_url 变更会触发 sitemap 重建入队），确保随后跑的是邮件任务
	for {
		ran, err := a.Jobs.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("排空后台任务失败: %v", err)
		}
		if !ran {
			break
		}
	}
	request(http.MethodPost, "/api/v1/auth/password/forgot", map[string]any{"email": "alice@test.local"}, "")
	runNextJob()
	if !strings.Contains(recorder.sends[len(recorder.sends)-1], "https://knowforge.example.com/reset-password?token=") {
		t.Fatalf("邮件链接应使用 site_url: %q", recorder.sends[len(recorder.sends)-1])
	}
}

// extractResetToken 从邮件正文中提取重置令牌
func extractResetToken(body string) string {
	marker := "/reset-password?token="
	idx := strings.Index(body, marker)
	if idx < 0 {
		return ""
	}
	rest := body[idx+len(marker):]
	end := strings.IndexAny(rest, "\"'<>& ")
	if end < 0 {
		return rest
	}
	return rest[:end]
}
