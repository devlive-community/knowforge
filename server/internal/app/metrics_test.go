package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"knowforge/server/internal/ai"
	"knowforge/server/internal/auth"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// 运行指标：默认关闭（404）；开启时生成令牌（只显示一次），无令牌或令牌错误 401；输出请求、事件流连接、模型调用、
// 任务队列与数据总量等指标；重新生成令牌后旧令牌失效；只有管理员能修改设置。
func TestMetricsEndpoint(t *testing.T) {
	a, h := newBackupTestApp(t, t.TempDir())
	_, installed := h.do("", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"指标站"},"admin":{"username":"mt-admin","email":"mt-admin@test.local","password":"secret123"}}`)
	adminToken := backupData(installed)["token"].(string)
	user := &models.User{Username: "mt-user", Email: "mt-user@test.local", Role: "user", IsActive: true, EmailVerified: true}
	a.DB.Create(user)
	userToken, _ := auth.GenerateToken(a.Config.Secret, user.ID, user.Username, user.Role)

	scrape := func(token string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, h.server.URL+"/metrics", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	if status, _ := scrape(""); status != http.StatusNotFound {
		t.Fatalf("未开启时应为 404: %d", status)
	}
	if status, _ := h.do(userToken, http.MethodPut, "/api/v1/metrics/settings", `{"enabled":true}`); status != http.StatusForbidden {
		t.Fatalf("普通用户不能开启运行指标: %d", status)
	}
	status, p := h.do(adminToken, http.MethodPut, "/api/v1/metrics/settings", `{"enabled":true}`)
	token, _ := backupData(p)["token"].(string)
	if status != http.StatusOK || !strings.HasPrefix(token, "kfm_") || backupData(p)["token_hint"] != token[len(token)-4:] {
		t.Fatalf("开启时应生成令牌: %d %v", status, p)
	}
	if _, p := h.do(adminToken, http.MethodGet, "/api/v1/metrics/settings", ""); backupData(p)["token"] != nil || backupData(p)["token_set"] != true {
		t.Fatalf("设置中不应再返回令牌: %v", p)
	}
	if status, _ := scrape(""); status != http.StatusUnauthorized {
		t.Fatalf("没有令牌应为 401: %d", status)
	}
	if status, _ := scrape("kfm_wrong"); status != http.StatusUnauthorized {
		t.Fatalf("令牌错误应为 401: %d", status)
	}

	// 指标为进程级计数（其他测试也会累加），断言事件流不计入耗时时比较打开前后的变化
	streamCount := func(body string) string {
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, `knowforge_http_request_duration_seconds_count{method="GET",route="/api/v1/notifications/stream"}`) {
				return line
			}
		}
		return ""
	}
	_, before := scrape(token)

	// 一个打开中的事件流
	_, tp := h.do(userToken, http.MethodPost, "/api/v1/stream-tickets", "")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel) // 断言失败提前退出时也要断开，否则测试服务器关闭时会一直等待这个连接
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, h.server.URL+"/api/v1/notifications/stream?ticket="+backupData(tp)["ticket"].(string), nil)
	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	// 一次模型调用（所有模型调用都经由用量记录）
	a.recordAIUsage(ai.Caller{Feature: "qa.ask"}, "chat", "openai", "metrics-model", ai.Usage{InputTokens: 120, OutputTokens: 30}, 0, 2*time.Second, nil)

	status, body := scrape(token)
	if status != http.StatusOK {
		t.Fatalf("抓取失败: %d %s", status, body)
	}
	for _, want := range []string{
		`knowforge_build_info{version="` + Version + `"`,
		`knowforge_http_requests_total{method="PUT",route="/api/v1/metrics/settings",status="200"}`,
		`knowforge_http_request_duration_seconds_bucket{method="PUT",route="/api/v1/metrics/settings",le="+Inf"}`,
		`knowforge_sse_connections{route="/api/v1/notifications/stream"} 1`,
		`knowforge_ai_calls_total{feature="qa.ask",kind="chat",model="metrics-model",status="ok"}`,
		`knowforge_ai_tokens_total{kind="chat",direction="input"}`,
		"knowforge_installed 1",
		"knowforge_jobs_oldest_waiting_seconds",
		"knowforge_cluster_instances 1",
		"knowforge_users 2",
		`knowforge_db_connections{state="in_use"}`,
		"# TYPE knowforge_http_request_duration_seconds histogram",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("指标中缺少 %s:\n%s", want, body)
		}
	}
	// 断开后连接数归零；事件流不计入耗时直方图
	cancel()
	stream.Body.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, body = scrape(token)
		if strings.Contains(body, `knowforge_sse_connections{route="/api/v1/notifications/stream"} 0`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("断开事件流后连接数应归零:\n%s", body)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if streamCount(body) != streamCount(before) {
		t.Fatalf("事件流不应计入请求耗时: %q → %q", streamCount(before), streamCount(body))
	}

	// 重新生成令牌：旧令牌失效
	_, p = h.do(adminToken, http.MethodPost, "/api/v1/metrics/token", "")
	newToken := backupData(p)["token"].(string)
	if status, _ := scrape(token); status != http.StatusUnauthorized {
		t.Fatalf("旧令牌应失效: %d", status)
	}
	if status, _ := scrape(newToken); status != http.StatusOK {
		t.Fatalf("新令牌应可用: %d", status)
	}
	// 管理员预览不需要令牌；关闭后 404
	if status, _ := h.do(adminToken, http.MethodGet, "/api/v1/metrics/preview", ""); status != http.StatusOK {
		t.Fatalf("管理员预览失败: %d", status)
	}
	h.do(adminToken, http.MethodPut, "/api/v1/metrics/settings", `{"enabled":false}`)
	if status, _ := scrape(newToken); status != http.StatusNotFound {
		t.Fatalf("关闭后应为 404: %d", status)
	}
}
