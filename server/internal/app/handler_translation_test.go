package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"knowforge/server/internal/ai"
	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

func TestTranslationConfig(t *testing.T) {
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
	req := func(method, path string, body any, token string) (int, map[string]any) {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		r, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		p := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&p)
		return resp.StatusCode, p
	}

	_, installed := req(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "翻译测试"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	token := installed["data"].(map[string]any)["token"].(string)

	// 未配置时 /translate 应 403，/site 的 translation_enabled 为 false
	if status, _ := req(http.MethodPost, "/api/v1/translate", map[string]any{"text": "hi", "target_label": "简体中文"}, token); status != http.StatusForbidden {
		t.Fatalf("未配置翻译应 403，实际 %d", status)
	}
	_, site := req(http.MethodGet, "/api/v1/site", nil, "")
	if site["data"].(map[string]any)["translation_enabled"] != false {
		t.Fatalf("未配置时 translation_enabled 应为 false")
	}

	// 非法 provider 应 400
	if status, _ := req(http.MethodPut, "/api/v1/translation", map[string]any{"provider": "bogus"}, token); status != http.StatusBadRequest {
		t.Fatalf("非法翻译方式应 400，实际 %d", status)
	}

	// 配置 openai + key 后，translation_enabled 应为 true，管理端可回读配置
	if status, _ := req(http.MethodPut, "/api/v1/translation", map[string]any{"provider": "openai", "api_key": "sk-test", "model": "gpt-4o-mini"}, token); status != http.StatusOK {
		t.Fatalf("保存翻译配置应成功，实际 %d", status)
	}
	_, got := req(http.MethodGet, "/api/v1/translation", nil, token)
	if got["data"].(map[string]any)["provider"] != "openai" || got["data"].(map[string]any)["api_key"] != "sk-test" {
		t.Fatalf("翻译配置回读不一致: %v", got["data"])
	}
	_, site2 := req(http.MethodGet, "/api/v1/site", nil, "")
	if site2["data"].(map[string]any)["translation_enabled"] != true {
		t.Fatalf("配置后 translation_enabled 应为 true")
	}
	// 公开配置不应泄露 API Key
	if _, leaked := site2["data"].(map[string]any)["translation_api_key"]; leaked {
		t.Fatalf("公开 /site 不应包含 translation_api_key")
	}
}

func TestTranslationUsageRecorded(t *testing.T) {
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
	var seenInputs []string
	// 假模型服务：OpenAI 兼容对话（返回用量）与 Google 翻译
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			var body struct {
				Messages []struct {
					Content string `json:"content"`
				} `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			input := body.Messages[len(body.Messages)-1].Content
			content := "Hello world"
			if strings.Contains(input, "⟦") { // 含占位符：回显（模拟保留占位符的翻译），「丢占位」时丢掉占位符
				seenInputs = append(seenInputs, input)
				content = "EN " + input
				if strings.Contains(input, "丢占位") {
					content = regexp.MustCompile(`⟦\d+⟧`).ReplaceAllString(content, "")
				}
			}
			raw, _ := json.Marshal(map[string]any{"model": "gpt-x", "usage": map[string]any{"prompt_tokens": 50, "completion_tokens": 12},
				"choices": []map[string]any{{"message": map[string]any{"content": content}}}})
			_, _ = w.Write(raw)
		case "/language/translate/v2":
			_, _ = w.Write([]byte(`{"data":{"translations":[{"translatedText":"Bonjour"}]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer fake.Close()
	client := &http.Client{Timeout: 10 * time.Second}
	req := func(method, path string, body any, token string) (int, map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(body)
		r, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		p := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&p)
		return resp.StatusCode, p
	}
	_, installed := req(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t), "site": map[string]any{"name": "翻译用量"},
		"admin": map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	admin := installed["data"].(map[string]any)["token"].(string)
	writer := &models.User{Username: "writer", Email: "writer@test.local", Role: "user", IsActive: true, EmailVerified: true}
	if err := a.DB.Create(writer).Error; err != nil {
		t.Fatal(err)
	}
	token, _ := auth.GenerateToken(a.Config.Secret, writer.ID, writer.Username, writer.Role)
	req(http.MethodPut, "/api/v1/admin/ai", map[string]any{"price_input": "1", "price_output": "2", "price_translate": "20"}, admin)

	// OpenAI 方式：经统一的 AI 客户端调用并记录 tokens 与字数
	req(http.MethodPut, "/api/v1/translation", map[string]any{"provider": "openai", "api_key": "sk", "api_base": fake.URL + "/v1", "model": "gpt-x"}, admin)
	status, p := req(http.MethodPost, "/api/v1/translate", map[string]any{"text": "你好世界", "target_label": "English", "ref_type": "document", "ref_id": 9}, token)
	if status != http.StatusOK || p["data"].(map[string]any)["text"] != "Hello world" {
		t.Fatalf("OpenAI 翻译失败: %d %v", status, p)
	}
	var row models.AIUsageLog
	a.DB.Last(&row)
	if row.Feature != "translate" || row.Kind != "chat" || row.UserID != writer.ID || row.InputTokens != 50 || row.OutputTokens != 12 ||
		row.Characters != 4 || row.RefType != "document" || row.RefID != 9 || row.CostMicros != 50*1+12*2 || row.Model != "gpt-x" {
		t.Fatalf("OpenAI 翻译用量记录异常: %+v", row)
	}

	// Google 方式：按字符记录与计价
	req(http.MethodPut, "/api/v1/translation", map[string]any{"provider": "google", "api_base": fake.URL}, admin)
	if status, p := req(http.MethodPost, "/api/v1/translate", map[string]any{"text": "Hello", "target_lang": "fr", "ref_type": "bogus"}, token); status != http.StatusOK {
		t.Fatalf("Google 翻译失败: %d %v", status, p)
	}
	row = models.AIUsageLog{} // Last 会以已有主键为条件，需重置
	a.DB.Last(&row)
	if row.Kind != "translate" || row.Provider != "google" || row.Characters != 5 || row.InputTokens != 0 || row.CostMicros != 5*20 || row.RefType != "" {
		t.Fatalf("Google 翻译用量记录异常: %+v", row)
	}

	// 每月翻译字数权益：剩余不足时拒绝，不产生调用
	if err := a.SetSetting(cfgTranslateMonthlyChars, "12", ""); err != nil {
		t.Fatal(err)
	}
	var before int64
	a.DB.Model(&models.AIUsageLog{}).Count(&before)
	status, p = req(http.MethodPost, "/api/v1/translate", map[string]any{"text": "abcdef", "target_lang": "fr"}, token)
	if status != http.StatusTooManyRequests || !strings.Contains(p["message"].(string), "剩余 3 字") {
		t.Fatalf("超出翻译字数应 429: %d %v", status, p)
	}
	var after int64
	a.DB.Model(&models.AIUsageLog{}).Count(&after)
	if after != before {
		t.Fatal("被额度拒绝的翻译不应调用模型")
	}
	_, mine := req(http.MethodGet, "/api/v1/users/me/ai-usage", nil, token)
	if d := mine["data"].(map[string]any); d["translate_chars"].(float64) != 9 || d["translate_limit"].(float64) != 12 || d["used_tokens"].(float64) != 62 {
		t.Fatalf("我的用量异常: %v", d)
	}

	// 同一调用链的多次调用归为一组；用户视图不含费用与原始错误信息
	ctx := ai.WithCaller(context.Background(), ai.Caller{UserID: writer.ID, Feature: "qa.agent", TraceID: "trace-1"})
	chatCfg := ai.Config{Provider: ai.ProviderOpenAI, BaseURL: fake.URL + "/v1", APIKey: "sk", Model: "gpt-x"}
	for i := 0; i < 2; i++ {
		if _, err := a.meteredChat(ctx, chatCfg, ai.ChatRequest{Messages: []ai.Message{{Role: "user", Content: "hi"}}}, 0); err != nil {
			t.Fatal(err)
		}
	}
	_, logs := req(http.MethodGet, "/api/v1/users/me/ai-usage/logs", nil, token)
	groups := logs["data"].(map[string]any)["items"].([]any)
	if logs["data"].(map[string]any)["total"].(float64) != 3 || len(groups) != 3 {
		t.Fatalf("应按调用链分为 3 组（2 次翻译 + 1 条问答链）: %v", logs)
	}
	first := groups[0].(map[string]any)
	if first["trace_id"] != "trace-1" || first["calls"].(float64) != 2 || first["input_tokens"].(float64) != 100 || len(first["items"].([]any)) != 2 {
		t.Fatalf("最新的调用链异常: %v", first)
	}
	if _, leaked := first["items"].([]any)[0].(map[string]any)["cost_micros"]; leaked {
		t.Fatal("用户视图不应包含费用")
	}
	_, one := req(http.MethodGet, "/api/v1/users/me/ai-usage/logs?trace_id=trace-1", nil, token)
	if one["data"].(map[string]any)["total"].(float64) != 1 {
		t.Fatalf("按调用链筛选异常: %v", one)
	}
	adminToken, _ := auth.GenerateToken(a.Config.Secret, 1, "admin", "admin")
	_, other := req(http.MethodGet, "/api/v1/users/me/ai-usage/logs?trace_id=trace-1", nil, adminToken)
	if other["data"].(map[string]any)["total"].(float64) != 0 {
		t.Fatal("不能看到他人的调用记录")
	}
	_, mineDaily := req(http.MethodGet, "/api/v1/users/me/ai-usage", nil, token)
	if len(mineDaily["data"].(map[string]any)["daily"].([]any)) == 0 {
		t.Fatal("应返回本月每日用量")
	}

	// AI 翻译同样受每月 AI 用量约束
	a.SetSetting(cfgTranslateMonthlyChars, "-1", "")
	a.SetSetting(cfgAIMonthlyTokens, "60", "")
	req(http.MethodPut, "/api/v1/translation", map[string]any{"provider": "openai"}, admin)
	if status, p := req(http.MethodPost, "/api/v1/translate", map[string]any{"text": "再来", "target_label": "English"}, token); status != http.StatusTooManyRequests {
		t.Fatalf("超出每月 AI 用量应 429: %d %v", status, p)
	}

	// Markdown 语法不交给翻译，译文中原样还原；模型丢失占位符时重试后报错
	a.SetSetting(cfgAIMonthlyTokens, "-1", "")
	req(http.MethodPut, "/api/v1/translation", map[string]any{"provider": "openai", "api_base": fake.URL + "/v1"}, admin)
	status, p = req(http.MethodPost, "/api/v1/translate", map[string]any{"text": "见 [文档](doc:setup)\n\n[children]", "target_label": "English"}, token)
	if d, _ := p["data"].(map[string]any); status != http.StatusOK || d["text"] != "EN 见 [文档](doc:setup)\n\n[children]" {
		t.Fatalf("应保留 Markdown 语法: %d %v", status, p)
	}
	if strings.Contains(strings.Join(seenInputs, "\n"), "children") || strings.Contains(strings.Join(seenInputs, "\n"), "doc:setup") {
		t.Fatalf("语法不应交给模型: %v", seenInputs)
	}
	if status, p := req(http.MethodPost, "/api/v1/translate", map[string]any{"text": "丢占位 [children]", "target_label": "English"}, token); status != http.StatusBadGateway || !strings.Contains(p["message"].(string), "Markdown 语法") {
		t.Fatalf("丢失占位符应报错: %d %v", status, p)
	}
}
