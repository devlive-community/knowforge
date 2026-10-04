package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIToolCallRoundTrip(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer sk" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		_, _ = w.Write([]byte(`{"model":"m-2026","usage":{"prompt_tokens":120,"completion_tokens":8},"choices":[{"message":{"content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"search_book","arguments":"{\"query\":\"安装\"}"}}]}}]}`))
	}))
	defer srv.Close()
	cfg := Config{Provider: ProviderOpenAI, BaseURL: srv.URL + "/v1", APIKey: "sk", Model: "m"}
	res, err := Chat(context.Background(), cfg, ChatRequest{
		System: "sys",
		Messages: []Message{
			{Role: "user", Content: "怎么安装？"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_0", Name: "get_toc", Arguments: json.RawMessage(`{}`)}}},
			{Role: "tool", ToolCallID: "call_0", Content: "目录"},
		},
		Tools: []Tool{{Name: "search_book", Description: "d", Parameters: map[string]any{"type": "object"}}},
	})
	if err != nil || len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != "search_book" || string(res.ToolCalls[0].Arguments) != `{"query":"安装"}` {
		t.Fatalf("工具调用解析错误: %v %+v", err, res)
	}
	if res.Model != "m-2026" || res.Usage.InputTokens != 120 || res.Usage.OutputTokens != 8 || res.Usage.Estimated {
		t.Fatalf("用量解析错误: %+v", res)
	}
	msgs := got["messages"].([]any)
	if len(msgs) != 4 || msgs[0].(map[string]any)["role"] != "system" || msgs[3].(map[string]any)["tool_call_id"] != "call_0" {
		t.Fatalf("请求消息格式错误: %v", msgs)
	}
	if tools := got["tools"].([]any); tools[0].(map[string]any)["type"] != "function" {
		t.Fatalf("工具格式错误: %v", tools)
	}
}

func TestAnthropicMergesToolResults(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "ak" || r.Header.Get("anthropic-version") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"根据[1]"},{"type":"tool_use","id":"tu_2","name":"read_section","input":{"id":3}}]}`))
	}))
	defer srv.Close()
	cfg := Config{Provider: ProviderAnthropic, BaseURL: srv.URL, APIKey: "ak"}
	res, err := Chat(context.Background(), cfg, ChatRequest{
		System: "sys",
		Messages: []Message{
			{Role: "user", Content: "问题"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "a", Name: "x", Arguments: json.RawMessage(`{}`)}, {ID: "b", Name: "y", Arguments: json.RawMessage(`{"q":1}`)}}},
			{Role: "tool", ToolCallID: "a", Content: "ra"},
			{Role: "tool", ToolCallID: "b", Content: "rb"},
		},
		Tools: []Tool{{Name: "read_section", Parameters: map[string]any{"type": "object"}}},
	})
	if err != nil || res.Content != "根据[1]" || len(res.ToolCalls) != 1 || string(res.ToolCalls[0].Arguments) != `{"id":3}` {
		t.Fatalf("响应解析错误: %v %+v", err, res)
	}
	// 未返回 usage 时按文本估算
	if !res.Usage.Estimated || res.Usage.InputTokens == 0 || res.Usage.OutputTokens == 0 || res.Model != "claude-sonnet-5" {
		t.Fatalf("应估算用量并回填默认模型: %+v", res)
	}
	msgs := got["messages"].([]any)
	if len(msgs) != 3 || got["system"] != "sys" {
		t.Fatalf("消息应为 user/assistant/user 三条: %v", msgs)
	}
	results := msgs[2].(map[string]any)["content"].([]any)
	if len(results) != 2 || results[1].(map[string]any)["tool_use_id"] != "b" {
		t.Fatalf("连续工具结果应合并到同一条 user 消息: %v", results)
	}
	if tools := got["tools"].([]any); tools[0].(map[string]any)["input_schema"] == nil {
		t.Fatal("Anthropic 工具应使用 input_schema")
	}
}

func TestEmbedOrderAndAvailability(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"index":1,"embedding":[0,1]},{"index":0,"embedding":[1,0]}]}`))
	}))
	defer srv.Close()
	cfg := Config{Provider: ProviderOpenAI, BaseURL: srv.URL, APIKey: "sk", EmbedModel: "e"}
	vecs, usage, err := Embed(context.Background(), cfg, []string{"a", "b"})
	if err != nil || vecs[0][0] != 1 || vecs[1][1] != 1 {
		t.Fatalf("应按 index 还原顺序: %v %v", vecs, err)
	}
	if !usage.Estimated || usage.InputTokens != 2 {
		t.Fatalf("嵌入用量应按文本估算: %+v", usage)
	}
	if (Config{Provider: ProviderAnthropic, APIKey: "k", EmbedModel: "e"}).EmbedAvailable() {
		t.Fatal("Anthropic 对话服务需单独配置嵌入服务")
	}
	if (Config{Provider: ProviderOpenAI, APIKey: "k"}).EmbedAvailable() {
		t.Fatal("未配置嵌入模型时不可用")
	}
}

func TestEstimateTokensAndCaller(t *testing.T) {
	if n := EstimateTokens("数据湖 table"); n != 3+2 { // 三个汉字 + 「 table」六个字符约 2 个
		t.Fatalf("估算 = %d", n)
	}
	ctx := WithCaller(context.Background(), Caller{UserID: 7, Feature: "qa.ask", RefType: "book", RefID: 3})
	if c := CallerFrom(ctx); c.UserID != 7 || c.Feature != "qa.ask" {
		t.Fatalf("调用方 = %+v", c)
	}
	if CallerFrom(context.Background()).Feature != "" {
		t.Fatal("未标注时应为空")
	}
	if (Usage{InputTokens: 1, OutputTokens: 2}).Add(Usage{InputTokens: 3, Estimated: true}) != (Usage{InputTokens: 4, OutputTokens: 2, Estimated: true}) {
		t.Fatal("用量累加错误")
	}
}

func TestSpeechFallsBackToChatEndpoint(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("ID3-fake"))
	}))
	defer srv.Close()
	cfg := Config{Provider: ProviderOpenAI, BaseURL: srv.URL + "/v1", APIKey: "k", SpeechModel: "tts-1"}
	if !cfg.SpeechAvailable() || (Config{Provider: ProviderAnthropic, APIKey: "k", SpeechModel: "tts-1"}).SpeechAvailable() || (Config{APIKey: "k"}).SpeechAvailable() {
		t.Fatal("语音合成可用性判定不对")
	}
	res, err := Speech(context.Background(), cfg, SpeechRequest{Text: "你好", Voice: "alloy"})
	if err != nil || string(res.Audio) != "ID3-fake" || res.ContentType != "audio/mpeg" {
		t.Fatalf("合成结果不对: %+v %v", res, err)
	}
	if gotPath != "/v1/audio/speech" || gotAuth != "Bearer k" || !strings.Contains(gotBody, `"voice":"alloy"`) || !strings.Contains(gotBody, `"model":"tts-1"`) {
		t.Fatalf("请求不对: %s %s %s", gotPath, gotAuth, gotBody)
	}
}
