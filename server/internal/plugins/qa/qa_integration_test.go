package qa_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"knowforge/server/internal/app"
	"knowforge/server/internal/auth"
	"knowforge/server/internal/cluster"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins/qa"
	"knowforge/server/internal/testdb"
)

// 集成测试（经 HTTP，AI 服务用本地假服务器模拟 OpenAI 兼容接口）：索引与向量化、标准问答出处、划词提问、Agent 工具调用、每日额度、社区问答。

type fakeAI struct {
	mu         sync.Mutex
	prompts    []string // 每次对话请求中最后一条 user 消息
	embeds     int
	toolRounds int           // Agent 模式下连续请求工具的轮数（默认 1）
	sameArgs   bool          // 每轮请求完全相同的工具调用
	delay      time.Duration // 每次对话的响应延迟
}

func (f *fakeAI) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.embeds += len(req.Input)
		f.mu.Unlock()
		items := []map[string]any{}
		for i, text := range req.Input {
			// 按关键词构造的确定性向量
			items = append(items, map[string]any{"index": i, "embedding": []float32{
				float32(strings.Count(text, "缓存")), float32(strings.Count(text, "索引")), 0.1,
			}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": items})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Tools  []any `json:"tools"`
			Stream bool  `json:"stream"`
		}
		_ = json.Unmarshal(raw, &req)
		toolMsgs, lastUser := 0, ""
		for _, m := range req.Messages {
			if m.Role == "tool" {
				toolMsgs++
			}
			if m.Role == "user" {
				lastUser = m.Content
			}
		}
		f.mu.Lock()
		f.prompts = append(f.prompts, lastUser)
		rounds, same, delay := f.toolRounds, f.sameArgs, f.delay
		f.mu.Unlock()
		if rounds == 0 {
			rounds = 1
		}
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		msg := map[string]any{"role": "assistant", "content": "缓存用于加速读取 [1]。"}
		if len(req.Tools) > 0 && toolMsgs < rounds {
			args := fmt.Sprintf(`{"query":"索引 %d"}`, toolMsgs)
			if same || toolMsgs == 0 {
				args = `{"query":"索引"}`
			}
			msg = map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{
				"id": fmt.Sprintf("call_%d", toolMsgs), "type": "function", "function": map[string]any{"name": "search_book", "arguments": args},
			}}}
		} else if toolMsgs > 0 {
			msg["content"] = "索引帮助检索 [1]。"
		}
		if req.Stream { // 流式：文本分两段推送，工具调用一次推送，最后推用量
			w.Header().Set("Content-Type", "text/event-stream")
			write := func(v any) {
				raw, _ := json.Marshal(v)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
			}
			if calls, ok := msg["tool_calls"].([]map[string]any); ok {
				for i, c := range calls {
					c["index"] = i
				}
				write(map[string]any{"model": "fake-1", "choices": []map[string]any{{"delta": map[string]any{"tool_calls": calls}}}})
			} else {
				text := []rune(msg["content"].(string))
				half := len(text) / 2
				write(map[string]any{"model": "fake-1", "choices": []map[string]any{{"delta": map[string]any{"content": string(text[:half])}}}})
				write(map[string]any{"choices": []map[string]any{{"delta": map[string]any{"content": string(text[half:])}}}})
			}
			write(map[string]any{"choices": []any{}, "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 20}})
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "fake-1", "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 20}, "choices": []map[string]any{{"message": msg}}})
	})
	return mux
}

func (f *fakeAI) lastPrompt() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.prompts) == 0 {
		return ""
	}
	return f.prompts[len(f.prompts)-1]
}

type testEnv struct {
	app    *app.App
	db     *gorm.DB
	token  string
	server *httptest.Server
	client *http.Client
}

func newTestEnv(t *testing.T, aiURL string) *testEnv {
	t.Helper()
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e := &testEnv{app: a, server: httptest.NewServer(a.Router()), client: &http.Client{Timeout: 10 * time.Second}}
	t.Cleanup(e.server.Close)
	status, installed := e.req(t, "", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"问答测试"},"admin":{"username":"qa-admin","email":"qa-admin@test.local","password":"secret123"}}`)
	if status != http.StatusOK {
		t.Fatalf("安装失败: %d %v", status, installed)
	}
	e.token = installed["data"].(map[string]any)["token"].(string)
	e.db = a.DB
	if status, p := e.req(t, e.token, http.MethodPost, "/api/v1/admin/plugins/qa/install", ""); status != http.StatusOK {
		t.Fatalf("启用问答插件失败: %d %v", status, p)
	}
	body := fmt.Sprintf(`{"provider":"openai","base_url":%q,"api_key":"test-key","model":"fake","embed_model":"fake-embed"}`, aiURL+"/v1")
	if status, p := e.req(t, e.token, http.MethodPut, "/api/v1/admin/ai", body); status != http.StatusOK || data(p)["chat_available"] != true || data(p)["embed_available"] != true {
		t.Fatalf("配置 AI 服务失败: %d %v", status, p)
	}
	return e
}

func (e *testEnv) req(t *testing.T, token, method, path, body string) (int, map[string]any) {
	t.Helper()
	r, _ := http.NewRequest(method, e.server.URL+path, bytes.NewReader([]byte(body)))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := e.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	p := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&p)
	return resp.StatusCode, p
}

func (e *testEnv) as(t *testing.T, u *models.User, method, path, body string) (int, map[string]any) {
	t.Helper()
	token, _ := auth.GenerateToken(e.app.Config.Secret, u.ID, u.Username, u.Role)
	return e.req(t, token, method, path, body)
}

func (e *testEnv) user(t *testing.T, name string) *models.User {
	t.Helper()
	u := &models.User{Username: name, Email: name + "@test.local", Role: "user", IsActive: true, EmailVerified: true}
	if err := e.db.Create(u).Error; err != nil {
		t.Fatal(err)
	}
	return u
}

func (e *testEnv) runJobs(t *testing.T) {
	t.Helper()
	for i := 0; i < 20; i++ {
		ran, err := e.app.Jobs.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("任务失败: %v", err)
		}
		if !ran {
			return
		}
	}
}

func data(p map[string]any) map[string]any { d, _ := p["data"].(map[string]any); return d }

// ask 提问并等待后台回答结束（非 200 时直接返回）；返回的 data 为最终的问答记录。
func (e *testEnv) ask(t *testing.T, u *models.User, base, body string) (int, map[string]any) {
	t.Helper()
	status, p := e.as(t, u, http.MethodPost, base+"/ask", body)
	if status != http.StatusOK {
		return status, p
	}
	if data(p)["status"] != "running" {
		t.Fatalf("提问应立即返回进行中的记录: %v", p)
	}
	return status, e.waitAsk(t, u, uint(data(p)["id"].(float64)))
}

func (e *testEnv) waitAsk(t *testing.T, u *models.User, id uint) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, p := e.as(t, u, http.MethodGet, fmt.Sprintf("/api/v1/qa/asks/%d", id), "")
		if data(p)["status"] != "running" {
			return p
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("问答 %d 未在测试时限内结束", id)
	return nil
}

func TestQAFlow(t *testing.T) {
	fake := &fakeAI{}
	aiServer := httptest.NewServer(fake.handler())
	t.Cleanup(aiServer.Close)
	e := newTestEnv(t, aiServer.URL)
	author, reader, helper := e.user(t, "author"), e.user(t, "reader"), e.user(t, "helper")

	_, created := e.as(t, author, http.MethodPost, "/api/v1/books", `{"title":"存储原理","status":"published","is_public":true}`)
	bookID := uint(data(created)["id"].(float64))
	base := fmt.Sprintf("/api/v1/qa/books/%d", bookID)
	content := "开篇介绍。\n\n## 缓存\n\n缓存可以加速读取，热点数据放在内存中。\n\n```\n# 代码里的井号不是标题\n```\n\n## 索引 `B+ 树`\n\n索引帮助检索，数据库常用 B+ 树。"
	_, d := e.as(t, author, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), fmt.Sprintf(`{"title":"第一章","content":%q,"status":"published"}`, content))
	docID := uint(data(d)["id"].(float64))
	docSlug := data(d)["slug"].(string)
	e.as(t, author, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), `{"title":"草稿","content":"草稿里的缓存机密","status":"draft"}`)

	// 状态：AI 可用、默认每日额度 20
	status, st := e.as(t, reader, http.MethodGet, base+"/status", "")
	if status != http.StatusOK || data(st)["ai_available"] != true || data(st)["agent_available"] != true {
		t.Fatalf("状态异常: %d %v", status, st)
	}
	if q := data(st)["quota"].(map[string]any); q["used"].(float64) != 0 || q["limit"].(float64) != 20 {
		t.Fatalf("额度异常: %v", q)
	}

	// 标准问答：出处指向小节锚点；草稿不进入索引
	status, ask := e.ask(t, reader, base, `{"question":"缓存有什么用？"}`)
	if status != http.StatusOK {
		t.Fatalf("提问失败: %d %v", status, ask)
	}
	cites := data(ask)["citations"].([]any)
	if len(cites) != 1 || cites[0].(map[string]any)["anchor"] != "h-1" || cites[0].(map[string]any)["doc_slug"] != docSlug || cites[0].(map[string]any)["heading"] != "缓存" {
		t.Fatalf("出处异常: %v", cites)
	}
	if p := fake.lastPrompt(); !strings.Contains(p, "缓存可以加速读取") || strings.Contains(p, "机密") {
		t.Fatalf("检索片段异常: %s", p)
	}
	askID := uint(data(ask)["id"].(float64))

	// 后台向量化
	e.runJobs(t)
	var state qa.IndexState
	e.db.First(&state, bookID)
	if state.Chunks != 3 || state.Embedded != 3 || fake.embeds < 3 {
		t.Fatalf("索引状态异常: %+v embeds=%d", state, fake.embeds)
	}
	// 内容未变化时不重建、不重复向量化
	before := fake.embeds
	e.ask(t, reader, base, `{"question":"索引是什么"}`)
	e.runJobs(t)
	if fake.embeds != before+1 { // 仅查询向量
		t.Fatalf("不应重新向量化: %d -> %d", before, fake.embeds)
	}

	// 划词提问：包含选中文字的小节优先加入
	status, sel := e.ask(t, reader, base, fmt.Sprintf(`{"selection":"数据库常用 B+ 树","doc_id":%d}`, docID))
	if status != http.StatusOK || data(sel)["question"] != "请解释这段内容" {
		t.Fatalf("划词提问失败: %d %v", status, sel)
	}
	if p := fake.lastPrompt(); !strings.Contains(p, "「数据库常用 B+ 树」") || !strings.Contains(p, "[1] 第一章 › 索引 B+ 树") {
		t.Fatalf("划词片段异常: %s", p)
	}

	// Agent 模式：调用 search_book 后作答
	status, ag := e.ask(t, reader, base, `{"question":"索引怎么实现？","mode":"agent"}`)
	if status != http.StatusOK || data(ag)["steps"].(float64) != 1 || data(ag)["mode"] != "agent" {
		t.Fatalf("Agent 问答失败: %d %v", status, ag)
	}
	if c := data(ag)["citations"].([]any); len(c) == 0 || c[0].(map[string]any)["anchor"] != "h-2" {
		t.Fatalf("Agent 出处异常: %v", c)
	}

	// 每日额度
	if err := e.app.SetSetting("qa_ai_daily", "4", ""); err != nil {
		t.Fatal(err)
	}
	if status, p := e.ask(t, reader, base, `{"question":"再问一次"}`); status != http.StatusTooManyRequests {
		t.Fatalf("超出额度应 429: %d %v", status, p)
	}
	_, hist := e.as(t, reader, http.MethodGet, base+"/asks", "")
	if data(hist)["total"].(float64) != 4 {
		t.Fatalf("问答记录异常: %v", hist)
	}

	// 管理员关闭 Agent 模式
	e.req(t, e.token, http.MethodPut, "/api/v1/admin/qa/settings", `{"agent_enabled":false}`)
	e.app.SetSetting("qa_ai_daily", "100", "")
	if status, _ := e.ask(t, reader, base, `{"question":"x","mode":"agent"}`); status != http.StatusBadRequest {
		t.Fatalf("关闭 Agent 后应拒绝: %d", status)
	}
	if status, p := e.req(t, e.token, http.MethodPut, "/api/v1/admin/qa/settings", `{"top_k":20}`); status != http.StatusBadRequest {
		t.Fatalf("top_k 越界应拒绝: %d %v", status, p)
	}

	// 重建索引仅作者可用
	if status, _ := e.as(t, reader, http.MethodPost, base+"/reindex", ""); status != http.StatusForbidden {
		t.Fatalf("读者不能重建索引: %d", status)
	}
	if status, p := e.as(t, author, http.MethodPost, base+"/reindex", ""); status != http.StatusOK || data(p)["chunks"].(float64) != 3 {
		t.Fatalf("重建索引失败: %d %v", status, p)
	}

	// 社区问答：附带 AI 回答提问 → 作者收到通知 → 他人回答 → 提问者采纳
	status, q := e.as(t, reader, http.MethodPost, base+"/questions", fmt.Sprintf(`{"title":"缓存和索引的区别？","ask_id":%d}`, askID))
	qv := data(q)["question"].(map[string]any)
	if status != http.StatusOK || data(q)["held"] != false || qv["ai_answer"] != "缓存用于加速读取 [1]。" || len(qv["ai_citations"].([]any)) != 1 {
		t.Fatalf("提问失败: %d %v", status, q)
	}
	qid := uint(qv["id"].(float64))
	var notes int64
	e.db.Model(&models.Notification{}).Where("user_id = ?", author.ID).Count(&notes)
	if notes != 1 {
		t.Fatalf("作者应收到提问通知: %d", notes)
	}
	status, ans := e.as(t, helper, http.MethodPost, fmt.Sprintf("/api/v1/qa/questions/%d/answers", qid), `{"body":"缓存提速，索引加速查找。"}`)
	if status != http.StatusOK {
		t.Fatalf("回答失败: %d %v", status, ans)
	}
	aid := uint(data(ans)["answer"].(map[string]any)["id"].(float64))
	if status, _ := e.as(t, helper, http.MethodPost, fmt.Sprintf("/api/v1/qa/answers/%d/accept", aid), ""); status != http.StatusForbidden {
		t.Fatalf("回答者不能采纳自己的回答: %d", status)
	}
	if status, p := e.as(t, reader, http.MethodPost, fmt.Sprintf("/api/v1/qa/answers/%d/accept", aid), ""); status != http.StatusOK || data(p)["status"] != "resolved" {
		t.Fatalf("采纳失败: %d %v", status, p)
	}
	_, list := e.req(t, "", http.MethodGet, base+"/questions?filter=resolved", "")
	if data(list)["total"].(float64) != 1 {
		t.Fatalf("已解决列表异常: %v", list)
	}
	_, detail := e.req(t, "", http.MethodGet, fmt.Sprintf("/api/v1/qa/questions/%d", qid), "")
	if answers := data(detail)["answers"].([]any); len(answers) != 1 || answers[0].(map[string]any)["accepted"] != true {
		t.Fatalf("问题详情异常: %v", detail)
	}
	e.db.Model(&models.Notification{}).Where("user_id IN ?", []uint{reader.ID, helper.ID}).Count(&notes)
	if notes != 2 {
		t.Fatalf("回答/采纳通知异常: %d", notes)
	}
	// 删除被采纳的回答：问题回到待解决
	if status, _ := e.as(t, reader, http.MethodDelete, fmt.Sprintf("/api/v1/qa/answers/%d", aid), ""); status != http.StatusForbidden {
		t.Fatalf("提问者不能删除他人回答: %d", status)
	}
	e.as(t, author, http.MethodDelete, fmt.Sprintf("/api/v1/qa/answers/%d", aid), "")
	var after qa.Question
	e.db.First(&after, qid)
	if after.Status != "open" || after.AnswerCount != 0 || after.AcceptedAnswerID != 0 {
		t.Fatalf("删除回答后状态异常: %+v", after)
	}
	if status, _ := e.as(t, helper, http.MethodDelete, fmt.Sprintf("/api/v1/qa/questions/%d", qid), ""); status != http.StatusForbidden {
		t.Fatalf("他人不能删除问题: %d", status)
	}
	if status, _ := e.as(t, reader, http.MethodDelete, fmt.Sprintf("/api/v1/qa/questions/%d", qid), ""); status != http.StatusOK {
		t.Fatalf("提问者删除问题失败: %d", status)
	}
}

func TestQAPrivateBookHidden(t *testing.T) {
	fake := &fakeAI{}
	aiServer := httptest.NewServer(fake.handler())
	t.Cleanup(aiServer.Close)
	e := newTestEnv(t, aiServer.URL)
	author, stranger := e.user(t, "author"), e.user(t, "stranger")
	_, created := e.as(t, author, http.MethodPost, "/api/v1/books", `{"title":"私密书","status":"published","is_public":false}`)
	bookID := uint(data(created)["id"].(float64))
	if status, _ := e.as(t, stranger, http.MethodPost, fmt.Sprintf("/api/v1/qa/books/%d/ask", bookID), `{"question":"内容？"}`); status != http.StatusNotFound {
		t.Fatalf("私密书籍不应可问: %d", status)
	}
	if status, _ := e.req(t, "", http.MethodGet, fmt.Sprintf("/api/v1/qa/books/%d/questions", bookID), ""); status != http.StatusNotFound {
		t.Fatalf("私密书籍问题列表不应可见: %d", status)
	}
}

func TestQAUsageAndEntitlements(t *testing.T) {
	fake := &fakeAI{}
	aiServer := httptest.NewServer(fake.handler())
	t.Cleanup(aiServer.Close)
	e := newTestEnv(t, aiServer.URL)
	author, reader, other := e.user(t, "author"), e.user(t, "reader"), e.user(t, "other")
	// 单价：输入 2、输出 10（每百万 tokens）
	if status, p := e.req(t, e.token, http.MethodPut, "/api/v1/admin/ai", `{"price_currency":"cny","price_input":"2","price_output":"10"}`); status != http.StatusOK {
		t.Fatalf("保存单价失败: %d %v", status, p)
	}
	if status, _ := e.req(t, e.token, http.MethodPut, "/api/v1/admin/ai", `{"price_input":"abc"}`); status != http.StatusBadRequest {
		t.Fatalf("非法单价应拒绝: %d", status)
	}

	_, created := e.as(t, author, http.MethodPost, "/api/v1/books", `{"title":"用量之书","status":"published","is_public":true}`)
	bookID := uint(data(created)["id"].(float64))
	base := fmt.Sprintf("/api/v1/qa/books/%d", bookID)
	e.as(t, author, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), `{"title":"第一章","content":"## 缓存\n\n缓存可以加速读取。\n\n## 索引\n\n索引帮助检索。","status":"published"}`)

	// 标准问答：1 次模型调用，按服务返回的用量记账
	status, ask := e.ask(t, reader, base, `{"question":"缓存有什么用？"}`)
	if status != http.StatusOK || data(ask)["calls"].(float64) != 1 || data(ask)["input_tokens"].(float64) != 100 || data(ask)["output_tokens"].(float64) != 20 {
		t.Fatalf("问答用量异常: %d %v", status, ask)
	}
	// 深度模式：2 次模型调用（检索 + 作答）
	status, ag := e.ask(t, reader, base, `{"question":"索引怎么实现？","mode":"agent"}`)
	if status != http.StatusOK || data(ag)["calls"].(float64) != 2 || data(ag)["input_tokens"].(float64) != 200 {
		t.Fatalf("深度模式用量异常: %d %v", status, ag)
	}
	e.runJobs(t)

	var logs []models.AIUsageLog
	e.db.Order("id").Find(&logs)
	byFeature := map[string]int{}
	for _, l := range logs {
		byFeature[l.Feature]++
		if l.Kind == "chat" && (l.UserID != reader.ID || l.CostMicros != 100*2+20*10 || l.Currency != "CNY" || l.Model != "fake-1" || l.RefID != bookID) {
			t.Fatalf("对话用量记录异常: %+v", l)
		}
		if l.Feature == "qa.index" && (l.UserID != 0 || l.Kind != "embed" || !l.Estimated) {
			t.Fatalf("索引用量应记为系统调用: %+v", l)
		}
	}
	if byFeature["qa.ask"] != 1 || byFeature["qa.agent"] != 2 || byFeature["qa.index"] != 1 { // 首次提问时向量尚未计算，不产生查询向量调用
		t.Fatalf("按功能记录异常: %v", byFeature)
	}

	// 我的用量与管理端统计
	_, mine := e.as(t, reader, http.MethodGet, "/api/v1/users/me/ai-usage", "")
	if data(mine)["used_tokens"].(float64) < 360 || data(mine)["limit"].(float64) != -1 {
		t.Fatalf("我的用量异常: %v", mine)
	}
	_, sum := e.req(t, e.token, http.MethodGet, "/api/v1/admin/ai/usage?days=7", "")
	total := data(sum)["total"].(map[string]any)
	if total["cost_micros"].(float64) < 3*400 || len(data(sum)["daily"].([]any)) != 7 || data(sum)["currency"] != "CNY" {
		t.Fatalf("管理端统计异常: %v", sum)
	}
	if top := data(sum)["top_users"].([]any); len(top) == 0 || top[0].(map[string]any)["username"] != "reader" {
		t.Fatalf("用量最高用户异常: %v", top)
	}
	_, logPage := e.req(t, e.token, http.MethodGet, "/api/v1/admin/ai/usage/logs?feature=qa.agent&user=reader", "")
	if data(logPage)["total"].(float64) != 2 {
		t.Fatalf("明细筛选异常: %v", logPage)
	}

	// 深度模式权益为 0：不可用
	if err := e.app.SetSetting("qa_agent_daily", "0", ""); err != nil {
		t.Fatal(err)
	}
	if _, st := e.as(t, reader, http.MethodGet, base+"/status", ""); data(st)["agent_available"] != false {
		t.Fatalf("深度模式权益为 0 时不可用: %v", st)
	}
	if status, _ := e.ask(t, reader, base, `{"question":"x","mode":"agent"}`); status != http.StatusTooManyRequests {
		t.Fatalf("深度模式应被拒绝: %d", status)
	}

	// 每月 tokens 权益：已用超过额度后拒绝，且不计入当日次数
	if err := e.app.SetSetting("ai_monthly_tokens", "300", ""); err != nil {
		t.Fatal(err)
	}
	status, p := e.ask(t, reader, base, `{"question":"还能问吗？"}`)
	if status != http.StatusTooManyRequests || !strings.Contains(p["message"].(string)+fmt.Sprint(p["error"]), "本月") {
		t.Fatalf("超出每月额度应 429: %d %v", status, p)
	}
	_, quota := e.as(t, reader, http.MethodGet, "/api/v1/qa/me/quota", "")
	if data(quota)["used"].(float64) != 2 || data(quota)["agent_used"].(float64) != 1 {
		t.Fatalf("当日次数异常: %v", quota)
	}
	// 其他用户未超额度，不受影响
	if status, p := e.ask(t, other, base, `{"question":"缓存？"}`); status != http.StatusOK {
		t.Fatalf("其他用户应可提问: %d %v", status, p)
	}

	// 我的问答历史：跨书、可删除（只能删自己的）
	_, hist := e.as(t, reader, http.MethodGet, "/api/v1/qa/me/asks", "")
	items := data(hist)["items"].([]any)
	if data(hist)["total"].(float64) != 2 || items[0].(map[string]any)["book"].(map[string]any)["title"] != "用量之书" {
		t.Fatalf("我的问答历史异常: %v", hist)
	}
	first := uint(items[0].(map[string]any)["ask"].(map[string]any)["id"].(float64))
	if status, _ := e.as(t, other, http.MethodDelete, fmt.Sprintf("/api/v1/qa/asks/%d", first), ""); status != http.StatusNotFound {
		t.Fatalf("不能删除他人记录: %d", status)
	}
	if status, _ := e.as(t, reader, http.MethodDelete, fmt.Sprintf("/api/v1/qa/asks/%d", first), ""); status != http.StatusOK {
		t.Fatalf("删除记录失败: %d", status)
	}
	e.as(t, reader, http.MethodPost, base+"/questions", `{"title":"社区提问"}`)
	if _, mq := e.as(t, reader, http.MethodGet, "/api/v1/qa/me/questions", ""); data(mq)["total"].(float64) != 1 {
		t.Fatalf("我的提问异常: %v", mq)
	}
}

func TestQAAgentUnboundedTraceAndCancel(t *testing.T) {
	fake := &fakeAI{toolRounds: 9}
	aiServer := httptest.NewServer(fake.handler())
	t.Cleanup(aiServer.Close)
	e := newTestEnv(t, aiServer.URL)
	author, reader, other := e.user(t, "author"), e.user(t, "reader"), e.user(t, "other")
	e.app.SetSetting("qa_agent_daily", "-1", "")
	_, created := e.as(t, author, http.MethodPost, "/api/v1/books", `{"title":"链路之书","status":"published","is_public":true}`)
	bookID := uint(data(created)["id"].(float64))
	base := fmt.Sprintf("/api/v1/qa/books/%d", bookID)
	e.as(t, author, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), `{"title":"第一章","content":"## 缓存\n\n缓存可以加速读取。\n\n## 索引\n\n索引帮助检索。","status":"published"}`)

	// 深度模式不限轮数：9 轮工具调用 + 最终作答 = 10 次模型调用（超过原先 7 次上限）
	status, p := e.ask(t, reader, base, `{"question":"索引怎么实现？","mode":"agent"}`)
	ag := data(p)
	if status != http.StatusOK || ag["status"] != "done" || ag["steps"].(float64) != 9 || ag["calls"].(float64) != 10 {
		t.Fatalf("深度模式应不限轮数: %d %v", status, p)
	}
	trace := ag["trace"].([]any)
	modelSteps, tools := 0, 0
	for _, raw := range trace {
		st := raw.(map[string]any)
		switch st["type"] {
		case "model":
			modelSteps++
			if st["model"] != "fake-1" || st["input_tokens"].(float64) != 100 {
				t.Fatalf("模型步骤异常: %v", st)
			}
		case "tool":
			tools++
			if st["name"] != "search_book" || st["query"] == "" || st["mode"] == nil {
				t.Fatalf("工具步骤异常: %v", st)
			}
		}
	}
	last := trace[len(trace)-1].(map[string]any)
	if modelSteps != 10 || tools != 9 || last["type"] != "model" || last["tool_calls"] != nil {
		t.Fatalf("调用链异常: models=%d tools=%d last=%v", modelSteps, tools, last)
	}
	if first := trace[0].(map[string]any); first["type"] != "model" || len(first["tool_calls"].([]any)) != 1 {
		t.Fatalf("首个模型步骤应请求工具: %v", first)
	}
	// 与核心 AI 用量记录按调用链 ID 对应
	var chained int64
	e.db.Model(&models.AIUsageLog{}).Where("trace_id = ? AND kind = ?", ag["trace_id"], "chat").Count(&chained)
	if chained != 10 {
		t.Fatalf("核心用量记录应有 10 次对话调用，实际 %d", chained)
	}
	_, mine := e.as(t, reader, http.MethodGet, "/api/v1/users/me/ai-usage/logs?trace_id="+ag["trace_id"].(string), "")
	if g := data(mine)["items"].([]any); len(g) != 1 || g[0].(map[string]any)["calls"].(float64) < 10 {
		t.Fatalf("我的调用链异常: %v", mine)
	}
	// 他人不可查看
	if status, _ := e.as(t, other, http.MethodGet, fmt.Sprintf("/api/v1/qa/asks/%d", uint(ag["id"].(float64))), ""); status != http.StatusNotFound {
		t.Fatalf("不能查看他人的问答: %d", status)
	}

	// 完全相同的工具调用不重复执行
	fake.mu.Lock()
	fake.toolRounds, fake.sameArgs = 3, true
	fake.mu.Unlock()
	_, p = e.ask(t, reader, base, `{"question":"缓存呢？","mode":"agent"}`)
	dups := 0
	for _, raw := range data(p)["trace"].([]any) {
		if st := raw.(map[string]any); st["type"] == "tool" && st["note"] == "duplicate" {
			dups++
		}
	}
	if data(p)["status"] != "done" || dups != 2 {
		t.Fatalf("重复的工具调用应直接提示（2 次）: %d %v", dups, p)
	}

	// 标准模式调用链：检索 → 模型
	_, p = e.ask(t, reader, base, `{"question":"缓存有什么用？"}`)
	types := []string{}
	for _, raw := range data(p)["trace"].([]any) {
		types = append(types, raw.(map[string]any)["type"].(string))
	}
	if strings.Join(types, ",") != "retrieve,model" && strings.Join(types, ",") != "embed,retrieve,model" {
		t.Fatalf("标准模式调用链异常: %v", types)
	}

	// 取消进行中的问答
	fake.mu.Lock()
	fake.delay = 3 * time.Second
	fake.mu.Unlock()
	status, p = e.as(t, reader, http.MethodPost, base+"/ask", `{"question":"慢一点","mode":"agent"}`)
	id := uint(data(p)["id"].(float64))
	time.Sleep(100 * time.Millisecond)
	if status, _ := e.as(t, reader, http.MethodPost, fmt.Sprintf("/api/v1/qa/asks/%d/cancel", id), ""); status != http.StatusOK {
		t.Fatalf("取消失败: %d", status)
	}
	final := data(e.waitAsk(t, reader, id))
	if final["status"] != "canceled" || final["error"] != "已取消" {
		t.Fatalf("取消后状态异常: %v", final)
	}
	if status, _ := e.as(t, reader, http.MethodPost, fmt.Sprintf("/api/v1/qa/asks/%d/cancel", id), ""); status != http.StatusConflict {
		t.Fatalf("已结束的问答不能再取消: %d", status)
	}

	// 服务重启遗留的进行中记录由巡检标记为中断
	orphan := qa.Ask{BookID: bookID, UserID: reader.ID, Mode: "rag", Question: "遗留", Status: "running", CreatedAt: time.Now().Add(-time.Minute)}
	e.db.Create(&orphan)
	plugincore.FireJobQueueSweep(e.app, e.app.Jobs)
	e.db.First(&orphan, orphan.ID)
	if orphan.Status != "failed" || orphan.Error == "" {
		t.Fatalf("遗留记录应标记中断: %+v", orphan)
	}

	// 多实例：在其他在线实例上生成的问答不被巡检误判，可跨实例取消；该实例下线后才标记中断
	if err := cluster.Start(context.Background(), e.db, "test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cluster.Stop)
	e.db.Create(&cluster.Instance{ID: "peer", StartedAt: time.Now(), SeenAt: time.Now()})
	remote := qa.Ask{BookID: bookID, UserID: reader.ID, Mode: "rag", Question: "在其他实例上", Status: "running", Runner: "peer", CreatedAt: time.Now().Add(-time.Minute)}
	e.db.Create(&remote)
	plugincore.FireJobQueueSweep(e.app, e.app.Jobs)
	if e.db.First(&remote, remote.ID); remote.Status != "running" {
		t.Fatalf("其他在线实例上的问答不应被标记中断: %+v", remote)
	}
	if status, _ := e.as(t, reader, http.MethodPost, fmt.Sprintf("/api/v1/qa/asks/%d/cancel", remote.ID), ""); status != http.StatusOK {
		t.Fatalf("应可取消其他实例上的问答: %d", status)
	}
	e.db.Model(&cluster.Instance{}).Where("id = ?", "peer").Update("seen_at", time.Now().Add(-time.Hour))
	plugincore.FireJobQueueSweep(e.app, e.app.Jobs)
	if e.db.First(&remote, remote.ID); remote.Status != "failed" {
		t.Fatalf("执行实例下线后应标记中断: %+v", remote)
	}
}

// readSSE 读取事件流直到 done（或连接结束），返回按顺序的 (事件名, 数据)。
func readSSE(t *testing.T, e *testEnv, u *models.User, path string) (int, [][2]string) {
	t.Helper()
	_, issued := e.as(t, u, http.MethodPost, "/api/v1/stream-tickets", "")
	resp, err := e.client.Get(e.server.URL + path + "?ticket=" + data(issued)["ticket"].(string))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("应为事件流: %s", ct)
	}
	var events [][2]string
	name := ""
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			events = append(events, [2]string{name, strings.TrimPrefix(line, "data: ")})
			if name == "done" {
				return resp.StatusCode, events
			}
		}
	}
	return resp.StatusCode, events
}

func TestQAStreamProgress(t *testing.T) {
	fake := &fakeAI{toolRounds: 3, delay: 150 * time.Millisecond}
	aiServer := httptest.NewServer(fake.handler())
	t.Cleanup(aiServer.Close)
	e := newTestEnv(t, aiServer.URL)
	author, reader, other := e.user(t, "author"), e.user(t, "reader"), e.user(t, "other")
	_, created := e.as(t, author, http.MethodPost, "/api/v1/books", `{"title":"推送之书","status":"published","is_public":true}`)
	bookID := uint(data(created)["id"].(float64))
	base := fmt.Sprintf("/api/v1/qa/books/%d", bookID)
	e.as(t, author, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), `{"title":"第一章","content":"## 缓存\n\n缓存可以加速读取。\n\n## 索引\n\n索引帮助检索。","status":"published"}`)

	_, p := e.as(t, reader, http.MethodPost, base+"/ask", `{"question":"索引怎么实现？","mode":"agent"}`)
	id := uint(data(p)["id"].(float64))
	stream := fmt.Sprintf("/api/v1/qa/asks/%d/stream", id)
	if status, _ := readSSE(t, e, other, stream); status != http.StatusNotFound {
		t.Fatalf("不能订阅他人的问答: %d", status)
	}
	if resp, err := e.client.Get(e.server.URL + stream); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录应 401: %v %v", err, resp)
	}
	token, _ := auth.GenerateToken(e.app.Config.Secret, reader.ID, reader.Username, reader.Role)
	if resp, err := e.client.Get(e.server.URL + stream + "?token=" + token); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("URL 中的登录令牌不应被接受: %v %v", err, resp)
	}
	status, events := readSSE(t, e, reader, stream)
	if status != http.StatusOK || len(events) < 3 || events[0][0] != "snapshot" || events[len(events)-1][0] != "done" {
		t.Fatalf("事件序列异常: %d %v", status, events)
	}
	var snap struct {
		Trace []TraceStepJSON `json:"trace"`
	}
	_ = json.Unmarshal([]byte(events[0][1]), &snap)
	next := len(snap.Trace)
	steps := 0
	for _, ev := range events[1 : len(events)-1] {
		if ev[0] == "delta" || ev[0] == "reset" {
			continue
		}
		if ev[0] != "step" {
			t.Fatalf("中间事件应为 step/delta/reset: %v", ev)
		}
		var st struct {
			Index int `json:"index"`
		}
		_ = json.Unmarshal([]byte(ev[1]), &st)
		if st.Index < next { // 快照已包含的步骤，客户端去重
			continue
		}
		if st.Index != next {
			t.Fatalf("步骤应连续推送: 期望 %d 实际 %d", next, st.Index)
		}
		next++
		steps++
	}
	// 回答文本逐段推送：最后一次 reset 之后的 delta 拼起来即最终回答
	streamed, deltas := "", 0
	for _, ev := range events {
		switch ev[0] {
		case "delta":
			var d struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal([]byte(ev[1]), &d)
			streamed += d.Text
			deltas++
		case "reset":
			streamed = ""
		}
	}
	var done struct {
		Status string          `json:"status"`
		Trace  []TraceStepJSON `json:"trace"`
		Answer string          `json:"answer"`
	}
	_ = json.Unmarshal([]byte(events[len(events)-1][1]), &done)
	if deltas == 0 || streamed != done.Answer {
		t.Fatalf("流式片段应拼成最终回答: %d 段 %q vs %q", deltas, streamed, done.Answer)
	}
	if steps == 0 || done.Status != "done" || done.Answer == "" || len(done.Trace) != next {
		t.Fatalf("推送与最终结果不一致: steps=%d next=%d done=%+v", steps, next, done)
	}
	// 已结束的问答：立即推送快照与 done
	if _, again := readSSE(t, e, reader, stream); len(again) != 2 || again[0][0] != "snapshot" || again[1][0] != "done" {
		t.Fatalf("已结束问答的事件流异常: %v", again)
	}
}

// TraceStepJSON 仅用于测试中解析调用链长度。
type TraceStepJSON = map[string]any

// 社区问答治理：发布前经发布守卫（此处启用敏感词审核插件）审查；被拦截内容只对本人与管理员可见、通知延后到通过时；
// 回答数只统计公开回答；可被举报并下架（下架被采纳的回答时问题回到待解决）。
func TestQACommunityGovernance(t *testing.T) {
	fake := &fakeAI{}
	aiServer := httptest.NewServer(fake.handler())
	t.Cleanup(aiServer.Close)
	e := newTestEnv(t, aiServer.URL)
	author, reader, helper, other := e.user(t, "author"), e.user(t, "reader"), e.user(t, "helper"), e.user(t, "other")
	if status, p := e.req(t, e.token, http.MethodPost, "/api/v1/admin/plugins/moderation/install", ""); status != http.StatusOK {
		t.Fatalf("启用审核插件失败: %d %v", status, p)
	}
	e.req(t, e.token, http.MethodPost, "/api/v1/admin/moderation/words", `{"words":"违禁词"}`)
	_, created := e.as(t, author, http.MethodPost, "/api/v1/books", `{"title":"治理之书","status":"published","is_public":true}`)
	bookID := uint(data(created)["id"].(float64))
	base := fmt.Sprintf("/api/v1/qa/books/%d", bookID)
	notes := func(u *models.User) int64 {
		var n int64
		e.db.Model(&models.Notification{}).Where("user_id = ? AND type = ?", u.ID, "comment").Count(&n)
		return n
	}
	authorBefore := notes(author)

	// 命中敏感词的提问：待审核，对他人不可见，作者暂不收到通知
	status, held := e.as(t, reader, http.MethodPost, base+"/questions", `{"title":"这里有违禁词吗"}`)
	hq := data(held)["question"].(map[string]any)
	if status != http.StatusOK || data(held)["held"] != true || hq["visibility"] != "held" || data(held)["message"] == "" {
		t.Fatalf("命中敏感词应待审核: %d %v", status, held)
	}
	hqid := uint(hq["id"].(float64))
	if _, list := e.as(t, other, http.MethodGet, base+"/questions", ""); data(list)["total"].(float64) != 0 {
		t.Fatalf("待审核提问对他人不可见: %v", list)
	}
	if _, list := e.as(t, reader, http.MethodGet, base+"/questions", ""); data(list)["total"].(float64) != 1 {
		t.Fatalf("提问者本人应可见: %v", list)
	}
	if status, _ := e.as(t, other, http.MethodGet, fmt.Sprintf("/api/v1/qa/questions/%d", hqid), ""); status != http.StatusNotFound {
		t.Fatalf("他人不能打开待审核提问: %d", status)
	}
	if status, _ := e.as(t, other, http.MethodPost, "/api/v1/reports", fmt.Sprintf(`{"target_type":"qa_question","target_id":%d,"reason":"spam"}`, hqid)); status != http.StatusNotFound {
		t.Fatalf("不可见的内容不能被举报: %d", status)
	}
	if notes(author) != authorBefore {
		t.Fatal("待审核提问不应通知作者")
	}
	// 管理员在审核队列中看到它（带查看链接）并通过 → 公开并补发通知
	_, cases := e.req(t, e.token, http.MethodGet, "/api/v1/admin/moderation/cases?status=pending&kind=qa_question", "")
	items := data(cases)["items"].([]any)
	if len(items) != 1 || !strings.Contains(items[0].(map[string]any)["link"].(string), fmt.Sprintf("question=%d", hqid)) {
		t.Fatalf("审核队列应包含提问及链接: %v", cases)
	}
	caseID := uint(items[0].(map[string]any)["case"].(map[string]any)["id"].(float64))
	if status, p := e.req(t, e.token, http.MethodPost, fmt.Sprintf("/api/v1/admin/moderation/cases/%d/approve", caseID), `{}`); status != http.StatusOK {
		t.Fatalf("审核通过失败: %d %v", status, p)
	}
	if _, list := e.as(t, other, http.MethodGet, base+"/questions", ""); data(list)["total"].(float64) != 1 {
		t.Fatal("通过后应公开")
	}
	if notes(author) != authorBefore+1 {
		t.Fatal("通过后应补发提问通知")
	}

	// 正常回答立即公开；命中敏感词的回答待审核，不计入回答数、提问者看不到
	if status, p := e.as(t, helper, http.MethodPost, fmt.Sprintf("/api/v1/qa/questions/%d/answers", hqid), `{"body":"正常的回答"}`); status != http.StatusOK || data(p)["held"] != false {
		t.Fatalf("正常回答应直接公开: %d %v", status, p)
	}
	_, heldAns := e.as(t, helper, http.MethodPost, fmt.Sprintf("/api/v1/qa/questions/%d/answers", hqid), `{"body":"回答里有违禁词"}`)
	if data(heldAns)["held"] != true {
		t.Fatalf("命中敏感词的回答应待审核: %v", heldAns)
	}
	var q qa.Question
	e.db.First(&q, hqid)
	if q.AnswerCount != 1 {
		t.Fatalf("回答数只统计公开回答: %d", q.AnswerCount)
	}
	_, detail := e.as(t, reader, http.MethodGet, fmt.Sprintf("/api/v1/qa/questions/%d", hqid), "")
	if n := len(data(detail)["answers"].([]any)); n != 1 {
		t.Fatalf("提问者不应看到待审核的回答: %d", n)
	}
	_, mine := e.as(t, helper, http.MethodGet, fmt.Sprintf("/api/v1/qa/questions/%d", hqid), "")
	if n := len(data(mine)["answers"].([]any)); n != 2 {
		t.Fatalf("回答者本人应看到自己待审核的回答: %d", n)
	}
	heldAnsID := uint(data(heldAns)["answer"].(map[string]any)["id"].(float64))
	if status, _ := e.as(t, reader, http.MethodPost, fmt.Sprintf("/api/v1/qa/answers/%d/accept", heldAnsID), ""); status != http.StatusConflict {
		t.Fatalf("未公开的回答不能采纳: %d", status)
	}

	// 举报公开回答 → 下架：回答隐藏、回答数减少、采纳被撤销
	answers := data(detail)["answers"].([]any)
	pubAnsID := uint(answers[0].(map[string]any)["answer"].(map[string]any)["id"].(float64))
	e.as(t, reader, http.MethodPost, fmt.Sprintf("/api/v1/qa/answers/%d/accept", pubAnsID), "")
	status, rep := e.as(t, other, http.MethodPost, "/api/v1/reports", fmt.Sprintf(`{"target_type":"qa_answer","target_id":%d,"reason":"spam"}`, pubAnsID))
	if status != http.StatusOK {
		t.Fatalf("举报回答失败: %d %v", status, rep)
	}
	_, reports := e.req(t, e.token, http.MethodGet, "/api/v1/admin/reports?target_type=qa_answer", "")
	if data(reports)["total"].(float64) != 1 || !strings.Contains(fmt.Sprint(data(reports)["target_types"]), "qa_answer") {
		t.Fatalf("管理端应能按问答类型筛选举报: %v", reports)
	}
	if status, p := e.req(t, e.token, http.MethodPut, fmt.Sprintf("/api/v1/admin/reports/%d", uint(data(rep)["id"].(float64))), `{"resolution":"takedown"}`); status != http.StatusOK {
		t.Fatalf("下架失败: %d %v", status, p)
	}
	e.db.First(&q, hqid)
	var a qa.Answer
	e.db.First(&a, pubAnsID)
	if a.Visibility != "hidden" || q.AnswerCount != 0 || q.AcceptedAnswerID != 0 || q.Status != "open" {
		t.Fatalf("下架后状态异常: answer=%+v question=%+v", a, q)
	}

	// 关闭「审查用户内容」后不再拦截
	e.req(t, e.token, http.MethodPut, "/api/v1/admin/moderation/settings", `{"scope_ugc":false}`)
	if _, p := e.as(t, reader, http.MethodPost, base+"/questions", `{"title":"又一个违禁词"}`); data(p)["held"] != false {
		t.Fatalf("关闭用户内容审查后应直接公开: %v", p)
	}
}

// 调用链保留期：过期问答只清空调用链明细，问答与消耗合计保留；进行中的不受影响。
func TestQATraceRetention(t *testing.T) {
	fake := &fakeAI{}
	aiServer := httptest.NewServer(fake.handler())
	t.Cleanup(aiServer.Close)
	e := newTestEnv(t, aiServer.URL)
	reader := e.user(t, "reader")
	if status, _ := e.req(t, e.token, http.MethodPut, "/api/v1/admin/qa/settings", `{"trace_retention_days":3}`); status != http.StatusBadRequest {
		t.Fatalf("少于 7 天应被拒绝: %d", status)
	}
	if status, p := e.req(t, e.token, http.MethodPut, "/api/v1/admin/qa/settings", `{"trace_retention_days":10}`); status != http.StatusOK || data(p)["settings"].(map[string]any)["trace_retention_days"].(float64) != 10 {
		t.Fatalf("保存保留天数失败: %d %v", status, p)
	}
	old := qa.Ask{BookID: 1, UserID: reader.ID, Mode: "rag", Question: "旧", Answer: "答", Status: "done", Trace: `[{"type":"model"}]`, InputTokens: 100, CreatedAt: time.Now().AddDate(0, 0, -20)}
	recent := qa.Ask{BookID: 1, UserID: reader.ID, Mode: "rag", Question: "新", Answer: "答", Status: "done", Trace: `[{"type":"model"}]`, CreatedAt: time.Now().AddDate(0, 0, -2)}
	e.db.Create(&old)
	e.db.Create(&recent)
	plugincore.FireJobQueueSweep(e.app, e.app.Jobs)
	e.db.First(&old, old.ID)
	e.db.First(&recent, recent.ID)
	if old.Trace != "[]" || old.Answer != "答" || old.InputTokens != 100 || recent.Trace == "[]" {
		t.Fatalf("保留期清理异常: old=%+v recent=%+v", old, recent)
	}
}
