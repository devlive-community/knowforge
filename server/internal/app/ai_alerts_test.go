package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"knowforge/server/internal/ai"
	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// 用量预警：超过阈值时记录并通知管理员，同一对象只报一次；不限制调用本身。
func TestAIUsageAlerts(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Router())
	defer server.Close()
	resp, err := http.Post(server.URL+"/api/v1/setup/install", "application/json", strings.NewReader(
		`{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"预警测试"},"admin":{"username":"admin","email":"admin@test.local","password":"secret123"}}`))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("安装失败: %v %v", err, resp)
	}
	resp.Body.Close()
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"model":"m","usage":{"prompt_tokens":50,"completion_tokens":12},"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer fake.Close()
	user := &models.User{Username: "heavy", Email: "heavy@test.local", Role: "user", IsActive: true, EmailVerified: true}
	a.DB.Create(user)
	for k, v := range map[string]string{"ai_price_input": "1", "ai_price_output": "2", cfgAlertDailyCost: "0.0002", cfgAlertUserDailyTokens: "150", cfgAlertTraceTokens: "100"} {
		if err := a.SetSetting(k, v, ""); err != nil {
			t.Fatal(err)
		}
	}
	chatCfg := ai.Config{Provider: ai.ProviderOpenAI, BaseURL: fake.URL, APIKey: "k", Model: "m"}
	call := func(trace string) {
		ctx := ai.WithCaller(context.Background(), ai.Caller{UserID: user.ID, Feature: "qa.agent", TraceID: trace})
		if _, err := a.meteredChat(ctx, chatCfg, ai.ChatRequest{Messages: []ai.Message{{Role: "user", Content: "hi"}}}, 0); err != nil {
			t.Fatalf("预警不应限制调用: %v", err)
		}
	}
	alerts := func(kind string) int64 {
		var n int64
		a.DB.Model(&models.AIAlert{}).Where("kind = ?", kind).Count(&n)
		return n
	}
	call("t1") // 62 tokens，74 微单位
	if alerts(alertTraceTokens)+alerts(alertUserDailyTokens)+alerts(alertSiteDailyCost) != 0 {
		t.Fatal("未超过阈值不应预警")
	}
	call("t1") // 调用链 124 ≥ 100；用户 124；费用 148
	if alerts(alertTraceTokens) != 1 || alerts(alertUserDailyTokens) != 0 {
		t.Fatalf("调用链超过阈值应预警一次")
	}
	call("t1") // 调用链 186（不重复预警）；用户 186 ≥ 150；费用 222 ≥ 200
	call("t2")
	if alerts(alertTraceTokens) != 1 || alerts(alertUserDailyTokens) != 1 || alerts(alertSiteDailyCost) != 1 {
		t.Fatalf("预警应按对象去重: trace=%d user=%d cost=%d", alerts(alertTraceTokens), alerts(alertUserDailyTokens), alerts(alertSiteDailyCost))
	}
	var notes int64
	a.DB.Model(&models.Notification{}).Where("user_id = ? AND type = ?", 1, "system").Count(&notes)
	if notes < 3 {
		t.Fatalf("管理员应收到三类预警通知: %d", notes)
	}
	var costAlert models.AIAlert
	a.DB.Where("kind = ?", alertSiteDailyCost).First(&costAlert)
	if costAlert.Threshold != 200 || costAlert.Value < 200 || costAlert.Currency != "USD" {
		t.Fatalf("费用预警记录异常: %+v", costAlert)
	}
}

// 保留期与导出：超过保留天数的用量记录与预警被清理；用户只能导出自己的记录（不含费用），管理员按筛选导出。
func TestAIUsageRetentionAndExport(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Router())
	defer server.Close()
	resp, err := http.Post(server.URL+"/api/v1/setup/install", "application/json", strings.NewReader(
		`{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"保留测试"},"admin":{"username":"admin","email":"admin@test.local","password":"secret123"}}`))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("安装失败: %v %v", err, resp)
	}
	var installed struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&installed)
	resp.Body.Close()
	alice := &models.User{Username: "alice", Email: "alice@test.local", Role: "user", IsActive: true, EmailVerified: true}
	bob := &models.User{Username: "bob", Email: "bob@test.local", Role: "user", IsActive: true, EmailVerified: true}
	a.DB.Create(alice)
	a.DB.Create(bob)
	now := time.Now()
	for _, r := range []models.AIUsageLog{
		{UserID: alice.ID, Feature: "qa.ask", TraceID: "old", Kind: "chat", Model: "m", InputTokens: 10, Status: "ok", CostMicros: 5, Currency: "USD", CreatedAt: now.AddDate(0, 0, -100)},
		{UserID: alice.ID, Feature: "qa.ask", TraceID: "recent", Kind: "chat", Model: "m", InputTokens: 20, OutputTokens: 3, Status: "ok", CostMicros: 9, Currency: "USD", CreatedAt: now.AddDate(0, 0, -30)},
		{UserID: bob.ID, Feature: "translate", TraceID: "bob", Kind: "translate", Characters: 7, Status: "ok", Currency: "USD", CreatedAt: now},
	} {
		r := r
		a.DB.Create(&r)
	}
	a.DB.Create(&models.AIAlert{Kind: alertTraceTokens, Key: "old", CreatedAt: now.AddDate(0, 0, -100)})

	get := func(path, token string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, server.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(body)
	}
	aliceToken, _ := auth.GenerateToken(a.Config.Secret, alice.ID, alice.Username, alice.Role)
	status, csvText := get("/api/v1/users/me/ai-usage/export", aliceToken)
	if status != http.StatusOK || !strings.HasPrefix(csvText, "\xEF\xBB\xBFtime,trace_id") || !strings.Contains(csvText, "recent") || strings.Contains(csvText, "bob") || strings.Contains(csvText, "cost") {
		t.Fatalf("用户导出异常: %d %q", status, csvText)
	}
	status, adminCSV := get("/api/v1/admin/ai/usage/export?user=alice", installed.Data.Token)
	if status != http.StatusOK || !strings.Contains(adminCSV, ",alice,") || strings.Contains(adminCSV, ",bob,") || !strings.Contains(adminCSV, "0.000009") {
		t.Fatalf("管理端导出异常: %d %q", status, adminCSV)
	}

	// 保留期：少于 90 天的设置被拒绝；90 天时清理 100 天前的记录与预警
	for _, v := range []string{"30", "5000"} {
		req, _ := http.NewRequest(http.MethodPut, server.URL+"/api/v1/admin/ai", strings.NewReader(`{"usage_retention_days":"`+v+`"}`))
		req.Header.Set("Authorization", "Bearer "+installed.Data.Token)
		req.Header.Set("Content-Type", "application/json")
		res, _ := http.DefaultClient.Do(req)
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("保留天数 %s 应被拒绝: %d", v, res.StatusCode)
		}
		res.Body.Close()
	}
	a.SetSetting(cfgAIUsageRetentionDays, "90", "")
	if err := a.purgeExpiredAIUsage(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	var left, alertsLeft int64
	a.DB.Model(&models.AIUsageLog{}).Count(&left)
	a.DB.Model(&models.AIAlert{}).Count(&alertsLeft)
	if left != 2 || alertsLeft != 0 {
		t.Fatalf("应清理 100 天前的记录与预警: 剩余记录 %d 预警 %d", left, alertsLeft)
	}
}
