package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"knowforge/server/internal/ai"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

func TestMatchModelPrice(t *testing.T) {
	list := []aiModelPrice{{Model: "gpt-4o", Input: 2.5}, {Model: "gpt-4o*", Input: 1}, {Model: "gpt-4o-mini*", Input: 0.15}, {Model: "*", Input: 9}}
	for model, want := range map[string]float64{
		"gpt-4o": 2.5, "GPT-4O": 2.5, "gpt-4o-2024-08-06": 1, "gpt-4o-mini-2024-07-18": 0.15, "claude-3": 9,
	} {
		if p, ok := matchModelPrice(list, model); !ok || p.Input != want {
			t.Fatalf("%s 应匹配单价 %v，实际 %v %v", model, want, p, ok)
		}
	}
	if _, ok := matchModelPrice([]aiModelPrice{{Model: "gpt-4o"}}, "claude"); ok {
		t.Fatal("未设置的模型不应匹配")
	}
}

// 按模型单价：保存校验、调用时按模型单价估算费用（未设置的用默认单价）、列出最近调用过的模型、按新单价重算。
func TestAIModelPrices(t *testing.T) {
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
	var token string
	do := func(method, path, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		p := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&p)
		return resp.StatusCode, p
	}
	_, installed := do(http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"单价"},"admin":{"username":"price-admin","email":"price-admin@test.local","password":"secret123"}}`)
	token = installed["data"].(map[string]any)["token"].(string)
	do(http.MethodPut, "/api/v1/admin/ai", `{"price_input":"1","price_output":"2","price_currency":"USD"}`)

	for _, body := range []string{`{"items":[{"model":"","input":1}]}`, `{"items":[{"model":"a","input":-1}]}`, `{"items":[{"model":"a"},{"model":"A"}]}`, `{"items":[{"model":"[","input":1}]}`} {
		if status, _ := do(http.MethodPut, "/api/v1/admin/ai/model-prices", body); status != http.StatusBadRequest {
			t.Fatalf("应拒绝 %s: %d", body, status)
		}
	}
	a.recordAIUsage(aiCallerForTest(), "chat", "openai", "gpt-4o-mini", aiUsageForTest(1_000_000, 1_000_000), 0, 0, nil)
	var before models.AIUsageLog
	a.DB.Last(&before)
	if before.CostMicros != 3_000_000 {
		t.Fatalf("未设置模型单价时按默认单价（1 + 2）估算，实际 %d", before.CostMicros)
	}
	if status, p := do(http.MethodPut, "/api/v1/admin/ai/model-prices", `{"items":[{"model":"gpt-4o-mini*","input":0.15,"output":0.6}]}`); status != http.StatusOK {
		t.Fatalf("保存单价失败: %d %v", status, p)
	}
	a.recordAIUsage(aiCallerForTest(), "chat", "openai", "gpt-4o-mini-2024", aiUsageForTest(1_000_000, 1_000_000), 0, 0, nil)
	var after models.AIUsageLog
	a.DB.Last(&after)
	if after.CostMicros != 750_000 {
		t.Fatalf("应按模型单价（0.15 + 0.6）估算，实际 %d", after.CostMicros)
	}
	_, p := do(http.MethodGet, "/api/v1/admin/ai/model-prices", "")
	seen := p["data"].(map[string]any)["seen"].([]any)
	matched := map[string]any{}
	for _, s := range seen {
		m := s.(map[string]any)
		matched[m["model"].(string)] = m["matched"]
	}
	if matched["gpt-4o-mini"] != "gpt-4o-mini*" || len(seen) != 2 {
		t.Fatalf("最近调用过的模型应标注匹配的单价: %v", seen)
	}
	// 重算：之前按默认单价记的那条改为按模型单价
	if status, p := do(http.MethodPost, "/api/v1/admin/ai/model-prices/recalculate", `{"days":7}`); status != http.StatusOK || p["data"].(map[string]any)["updated"].(float64) != 1 {
		t.Fatalf("重算失败: %d %v", status, p)
	}
	a.DB.First(&before, before.ID)
	if before.CostMicros != 750_000 {
		t.Fatalf("重算后应按模型单价，实际 %d", before.CostMicros)
	}
}

func aiCallerForTest() ai.Caller { return ai.Caller{Feature: "admin.test"} }

func aiUsageForTest(in, out int64) ai.Usage { return ai.Usage{InputTokens: in, OutputTokens: out} }
