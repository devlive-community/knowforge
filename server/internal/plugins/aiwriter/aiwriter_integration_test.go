package aiwriter_test

import (
	"bufio"
	"bytes"
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
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// 集成测试（经 HTTP，AI 服务用本地假服务器模拟 OpenAI 兼容的流式接口）：动作与上下文、流式推送、取消、采纳、额度、权限与插件开关。

type fakeAI struct {
	mu      sync.Mutex
	systems []string
	prompts []string
	delay   time.Duration // 两段文本之间的间隔
}

func (f *fakeAI) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(raw, &req)
		f.mu.Lock()
		for _, m := range req.Messages {
			switch m.Role {
			case "system":
				f.systems = append(f.systems, m.Content)
			case "user":
				f.prompts = append(f.prompts, m.Content)
			}
		}
		delay := f.delay
		f.mu.Unlock()
		if !req.Stream {
			_ = json.NewEncoder(w).Encode(map[string]any{"model": "fake-1", "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 20},
				"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "处理后的文字。"}}}})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(v any) {
			raw, _ := json.Marshal(v)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
			w.(http.Flusher).Flush()
		}
		write(map[string]any{"model": "fake-1", "choices": []map[string]any{{"delta": map[string]any{"content": "处理后"}}}})
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		write(map[string]any{"choices": []map[string]any{{"delta": map[string]any{"content": "的文字。"}}}})
		write(map[string]any{"choices": []any{}, "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 20}})
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	})
	return mux
}

func (f *fakeAI) last() (string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.prompts) == 0 {
		return "", ""
	}
	return f.systems[len(f.systems)-1], f.prompts[len(f.prompts)-1]
}

func (f *fakeAI) setDelay(d time.Duration) {
	f.mu.Lock()
	f.delay = d
	f.mu.Unlock()
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
	status, installed := e.req(t, "", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"写作助手测试"},"admin":{"username":"w-admin","email":"w-admin@test.local","password":"secret123"}}`)
	if status != http.StatusOK {
		t.Fatalf("安装失败: %d %v", status, installed)
	}
	e.token = installed["data"].(map[string]any)["token"].(string)
	e.db = a.DB
	if status, p := e.req(t, e.token, http.MethodPost, "/api/v1/admin/plugins/ai-writer/install", ""); status != http.StatusOK {
		t.Fatalf("启用写作助手插件失败: %d %v", status, p)
	}
	body := fmt.Sprintf(`{"provider":"openai","base_url":%q,"api_key":"test-key","model":"fake"}`, aiURL+"/v1")
	if status, p := e.req(t, e.token, http.MethodPut, "/api/v1/admin/ai", body); status != http.StatusOK || data(p)["chat_available"] != true {
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

func tokenOf(e *testEnv, u *models.User) string {
	token, _ := auth.GenerateToken(e.app.Config.Secret, u.ID, u.Username, u.Role)
	return token
}

func (e *testEnv) as(t *testing.T, u *models.User, method, path, body string) (int, map[string]any) {
	t.Helper()
	return e.req(t, tokenOf(e, u), method, path, body)
}

func (e *testEnv) user(t *testing.T, name string) *models.User {
	t.Helper()
	u := &models.User{Username: name, Email: name + "@test.local", Role: "user", IsActive: true, EmailVerified: true}
	if err := e.db.Create(u).Error; err != nil {
		t.Fatal(err)
	}
	return u
}

func data(p map[string]any) map[string]any { d, _ := p["data"].(map[string]any); return d }

func id(p map[string]any) uint { return uint(data(p)["id"].(float64)) }

func (e *testEnv) wait(t *testing.T, u *models.User, taskID uint) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, p := e.as(t, u, http.MethodGet, fmt.Sprintf("/api/v1/ai-writer/tasks/%d", taskID), "")
		if data(p)["status"] != "running" {
			return data(p)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("任务 %d 未在测试时限内结束", taskID)
	return nil
}

func task(bookID, docID uint, action, text, extra string) string {
	body := fmt.Sprintf(`{"book_id":%d,"doc_id":%d,"action":%q,"text":%q`, bookID, docID, action, text)
	if extra != "" {
		body += "," + extra
	}
	return body + "}"
}

func TestAIWriterFlow(t *testing.T) {
	fake := &fakeAI{}
	aiServer := httptest.NewServer(fake.handler())
	t.Cleanup(aiServer.Close)
	e := newTestEnv(t, aiServer.URL)
	author, reader := e.user(t, "author"), e.user(t, "reader")

	_, created := e.as(t, author, http.MethodPost, "/api/v1/books", `{"title":"存储原理","status":"published","is_public":true}`)
	bookID := id(created)
	_, d := e.as(t, author, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), `{"title":"第一章 缓存","content":"正文","status":"published"}`)
	docID := id(d)

	status, st := e.as(t, author, http.MethodGet, "/api/v1/ai-writer/status", "")
	if status != http.StatusOK || data(st)["available"] != true {
		t.Fatalf("状态异常: %d %v", status, st)
	}
	if q := data(st)["quota"].(map[string]any); q["used"].(float64) != 0 || q["limit"].(float64) != 100 {
		t.Fatalf("默认额度异常: %v", q)
	}

	// 润色：立即返回进行中的任务，后台流式生成；提示词含动作要求、章节、上下文与原文
	status, p := e.as(t, author, http.MethodPost, "/api/v1/ai-writer/tasks",
		task(bookID, docID, "polish", "缓存能加快读区速度", `"before":"前面的段落。","after":"后面的段落。","instruction":"语气正式一些"`))
	if status != http.StatusOK || data(p)["status"] != "running" {
		t.Fatalf("创建任务失败: %d %v", status, p)
	}
	done := e.wait(t, author, id(p))
	if done["status"] != "done" || done["result"] != "处理后的文字。" || done["input_tokens"].(float64) != 100 || done["output_tokens"].(float64) != 20 || done["model"] != "fake-1" {
		t.Fatalf("结果异常: %v", done)
	}
	system, prompt := fake.last()
	for _, want := range []string{"润色", "第一章 缓存", "存储原理", "语气正式一些", "缓存能加快读区速度", "前面的段落。", "后面的段落。"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("提示词缺少 %q: %s", want, prompt)
		}
	}
	if !strings.Contains(system, "只输出处理后的正文") {
		t.Fatalf("系统提示异常: %s", system)
	}
	// 每次调用写入 AI 用量记录，按调用链关联到任务
	var usage models.AIUsageLog
	if err := e.db.Where("trace_id = ?", done["trace_id"]).First(&usage).Error; err != nil || usage.Feature != "aiwriter.polish" || usage.UserID != author.ID || usage.RefType != "document" || usage.RefID != docID {
		t.Fatalf("用量记录异常: %v %+v", err, usage)
	}

	// 续写：上文只取光标前的末尾部分
	long := strings.Repeat("甲", 7000) + "结尾句。"
	_, p = e.as(t, author, http.MethodPost, "/api/v1/ai-writer/tasks", task(bookID, docID, "continue", long, `"after":"下文开头"`))
	if done := e.wait(t, author, id(p)); done["status"] != "done" || len([]rune(done["input"].(string))) != 6000 {
		t.Fatalf("续写异常: %v", done["status"])
	}
	if _, prompt := fake.last(); !strings.Contains(prompt, "结尾句。") || !strings.Contains(prompt, "下文开头") || strings.Contains(prompt, strings.Repeat("甲", 6000)) {
		t.Fatalf("续写上文异常")
	}

	// 参数校验
	for _, c := range []struct{ body, msg string }{
		{task(bookID, docID, "poem", "文字", ""), "不支持的操作"},
		{task(bookID, docID, "custom", "文字", ""), "请填写要求"},
		{task(bookID, docID, "rewrite", "  ", ""), "请先选中要处理的文字"},
		{task(bookID, docID, "continue", "", ""), "光标前还没有内容"},
		{task(bookID, 99999, "rewrite", "文字", ""), "章节不存在"},
	} {
		if status, p := e.as(t, author, http.MethodPost, "/api/v1/ai-writer/tasks", c.body); status == http.StatusOK || !strings.Contains(p["message"].(string), c.msg) {
			t.Fatalf("应拒绝 %s: %d %v", c.body, status, p)
		}
	}
	// 只有能编辑本书的用户可用
	if status, _ := e.as(t, reader, http.MethodPost, "/api/v1/ai-writer/tasks", task(bookID, 0, "rewrite", "文字", "")); status != http.StatusForbidden {
		t.Fatalf("非作者应被拒绝: %d", status)
	}

	// 事件流：先推 snapshot，再逐段推 delta，结束推 done
	fake.setDelay(300 * time.Millisecond)
	_, p = e.as(t, author, http.MethodPost, "/api/v1/ai-writer/tasks", task(bookID, docID, "expand", "缓存", ""))
	streamID := id(p)
	r, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/v1/ai-writer/tasks/%d/stream", e.server.URL, streamID), nil)
	r.Header.Set("Authorization", "Bearer "+tokenOf(e, author))
	resp, err := e.client.Do(r)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("订阅失败: %v", err)
	}
	events, text := []string{}, ""
	sc := bufio.NewScanner(resp.Body)
	name := ""
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "event: ") {
			name = strings.TrimPrefix(line, "event: ")
			events = append(events, name)
		}
		if strings.HasPrefix(line, "data: ") {
			var ev map[string]any
			_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev)
			switch name {
			case "snapshot":
				text, _ = ev["result"].(string)
			case "delta":
				if int(ev["seq"].(float64)) > 0 {
					text += ev["text"].(string)
				}
			}
		}
		if name == "done" && strings.HasPrefix(line, "data: ") {
			break
		}
	}
	resp.Body.Close()
	if events[0] != "snapshot" || events[len(events)-1] != "done" || text != "处理后的文字。" {
		t.Fatalf("事件流异常: %v %q", events, text)
	}

	// 取消：保留已生成的部分
	fake.setDelay(5 * time.Second)
	_, p = e.as(t, author, http.MethodPost, "/api/v1/ai-writer/tasks", task(bookID, docID, "rewrite", "缓存", ""))
	cancelID := id(p)
	time.Sleep(200 * time.Millisecond)
	if status, _ := e.as(t, reader, http.MethodPost, fmt.Sprintf("/api/v1/ai-writer/tasks/%d/cancel", cancelID), ""); status != http.StatusNotFound {
		t.Fatalf("不能取消他人的任务: %d", status)
	}
	if status, p := e.as(t, author, http.MethodDelete, fmt.Sprintf("/api/v1/ai-writer/tasks/%d", cancelID), ""); status != http.StatusConflict {
		t.Fatalf("进行中的任务不能删除: %d %v", status, p)
	}
	if status, p := e.as(t, author, http.MethodPost, fmt.Sprintf("/api/v1/ai-writer/tasks/%d/cancel", cancelID), ""); status != http.StatusOK {
		t.Fatalf("取消失败: %d %v", status, p)
	}
	if canceled := e.wait(t, author, cancelID); canceled["status"] != "canceled" || canceled["result"] != "处理后" {
		t.Fatalf("取消后状态异常: %v", canceled)
	}
	fake.setDelay(0)

	// 采纳
	if status, p := e.as(t, author, http.MethodPost, "/api/v1/ai-writer/tasks/99999/adopt", `{"mode":"replace"}`); status != http.StatusNotFound {
		t.Fatalf("不存在的任务: %d %v", status, p)
	}
	status, p = e.as(t, author, http.MethodPost, fmt.Sprintf("/api/v1/ai-writer/tasks/%d/adopt", streamID), `{"mode":"insert"}`)
	if status != http.StatusOK || data(p)["adopted"] != "insert" || data(p)["adopted_at"] == nil {
		t.Fatalf("采纳失败: %d %v", status, p)
	}

	// 记录：按书筛选，新→旧；已取消且生成过内容的任务也计入本月次数
	_, list := e.as(t, author, http.MethodGet, fmt.Sprintf("/api/v1/ai-writer/tasks?book_id=%d", bookID), "")
	items := data(list)["items"].([]any)
	if data(list)["total"].(float64) != 4 || uint(items[0].(map[string]any)["id"].(float64)) != cancelID {
		t.Fatalf("记录异常: %v", data(list))
	}
	_, st = e.as(t, author, http.MethodGet, "/api/v1/ai-writer/status", "")
	if used := data(st)["quota"].(map[string]any)["used"].(float64); used != 4 {
		t.Fatalf("已用次数应为 4: %v", used)
	}

	// 每月次数为权益：用完后拒绝；0 表示不可用
	if status, p := e.req(t, e.token, http.MethodPut, "/api/v1/admin/entitlements/base", `{"values":{"aiwriter.monthly_uses":4}}`); status != http.StatusOK {
		t.Fatalf("设置基础额度失败: %d %v", status, p)
	}
	if status, p := e.as(t, author, http.MethodPost, "/api/v1/ai-writer/tasks", task(bookID, docID, "rewrite", "文字", "")); status != http.StatusTooManyRequests || !strings.Contains(p["message"].(string), "本月写作助手次数已用完") {
		t.Fatalf("超额应被拒绝: %d %v", status, p)
	}
	e.req(t, e.token, http.MethodPut, "/api/v1/admin/entitlements/base", `{"values":{"aiwriter.monthly_uses":0}}`)
	if status, p := e.as(t, author, http.MethodPost, "/api/v1/ai-writer/tasks", task(bookID, docID, "rewrite", "文字", "")); status != http.StatusTooManyRequests || !strings.Contains(p["message"].(string), "不含 AI 写作助手") {
		t.Fatalf("额度为 0 应不可用: %d %v", status, p)
	}

	// 删除
	if status, _ := e.as(t, author, http.MethodDelete, fmt.Sprintf("/api/v1/ai-writer/tasks/%d", cancelID), ""); status != http.StatusOK {
		t.Fatalf("删除失败: %d", status)
	}

	// 插件关闭后接口不可用
	if status, p := e.req(t, e.token, http.MethodPost, "/api/v1/admin/plugins/ai-writer/uninstall", ""); status != http.StatusOK {
		t.Fatalf("关闭插件失败: %d %v", status, p)
	}
	if status, _ := e.as(t, author, http.MethodGet, "/api/v1/ai-writer/status", ""); status != http.StatusNotFound {
		t.Fatalf("插件关闭后应返回 404: %d", status)
	}
}
