package chapterguide_test

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
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins/chapterguide"
	"knowforge/server/internal/testdb"
)

// 集成测试（经 HTTP，AI 服务用本地假服务器模拟）：批量生成与复用、过期与编辑、强制重新生成、全书概览、读者可见性、
// 费用承担方、每月次数权益、自动模式（首次发布与修改后巡检）、事件流与插件开关。

type fakeAI struct {
	mu      sync.Mutex
	calls   int
	prompts []string
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
		}
		_ = json.Unmarshal(raw, &req)
		system, user := "", ""
		for _, m := range req.Messages {
			if m.Role == "system" {
				system = m.Content
			} else if m.Role == "user" {
				user = m.Content
			}
		}
		f.mu.Lock()
		f.calls++
		n := f.calls
		f.prompts = append(f.prompts, user)
		f.mu.Unlock()
		out := "## 概览\n\n这本书讲存储。"
		if strings.Contains(system, "章节导读") {
			title := strings.TrimPrefix(strings.Split(strings.Split(user, "\n")[1], "\n")[0], "章节：")
			b, _ := json.Marshal(map[string]any{"summary": fmt.Sprintf("导读%d：%s", n, title), "points": []string{"要点一", " ", "要点二"}})
			out = "```json\n" + string(b) + "\n```"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "fake-1", "usage": map[string]any{"prompt_tokens": 50, "completion_tokens": 10},
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": out}}}})
	})
	return mux
}

func (f *fakeAI) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeAI) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	status, installed := e.req(t, "", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"导读测试"},"admin":{"username":"g-admin","email":"g-admin@test.local","password":"secret123"}}`)
	if status != http.StatusOK {
		t.Fatalf("安装失败: %d %v", status, installed)
	}
	e.token = installed["data"].(map[string]any)["token"].(string)
	e.db = a.DB
	if status, p := e.req(t, e.token, http.MethodPost, "/api/v1/admin/plugins/chapter-guide/install", ""); status != http.StatusOK {
		t.Fatalf("启用插件失败: %d %v", status, p)
	}
	body := fmt.Sprintf(`{"provider":"openai","base_url":%q,"api_key":"test-key","model":"fake"}`, aiURL+"/v1")
	if status, p := e.req(t, e.token, http.MethodPut, "/api/v1/admin/ai", body); status != http.StatusOK {
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

func (e *testEnv) runJobs(t *testing.T) {
	t.Helper()
	for i := 0; i < 50; i++ {
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

func num(v any) int { f, _ := v.(float64); return int(f) }

func (e *testEnv) doc(t *testing.T, u *models.User, bookID uint, body string) uint {
	t.Helper()
	status, p := e.as(t, u, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), body)
	if status != http.StatusOK {
		t.Fatalf("创建章节失败: %d %v", status, p)
	}
	return uint(num(data(p)["id"]))
}

// chapters 管理页的章节行（按章节 ID 索引）。
func (e *testEnv) chapters(t *testing.T, u *models.User, bookID uint) (map[uint]map[string]any, map[string]any) {
	t.Helper()
	status, p := e.as(t, u, http.MethodGet, fmt.Sprintf("/api/v1/chapter-guides/books/%d", bookID), "")
	if status != http.StatusOK {
		t.Fatalf("读取导读失败: %d %v", status, p)
	}
	out := map[uint]map[string]any{}
	for _, row := range data(p)["chapters"].([]any) {
		r := row.(map[string]any)
		g, _ := r["guide"].(map[string]any)
		out[uint(num(r["doc"].(map[string]any)["id"]))] = g
	}
	return out, data(p)
}

func TestChapterGuides(t *testing.T) {
	fake := &fakeAI{}
	aiServer := httptest.NewServer(fake.handler())
	t.Cleanup(aiServer.Close)
	e := newTestEnv(t, aiServer.URL)
	author, reader := e.user(t, "author"), e.user(t, "reader")
	_, created := e.as(t, author, http.MethodPost, "/api/v1/books", `{"title":"存储原理","description":"讲解存储","status":"published","is_public":true}`)
	bookID := uint(num(data(created)["id"]))
	base := fmt.Sprintf("/api/v1/chapter-guides/books/%d", bookID)
	ch1 := e.doc(t, author, bookID, `{"title":"缓存","content":"缓存可以加速读取。","status":"published"}`)
	ch2 := e.doc(t, author, bookID, `{"title":"索引","content":"索引帮助检索。","status":"published","sort_order":1}`)
	ch3 := e.doc(t, author, bookID, `{"title":"草稿章","content":"还在写。","status":"draft","sort_order":2}`)
	empty := e.doc(t, author, bookID, `{"title":"空章节","content":"","status":"published","sort_order":3}`)

	rows, meta := e.chapters(t, author, bookID)
	if len(rows) != 4 || meta["available"] != true || meta["auto_generate"] != false || meta["cost_bearer"] != "author" ||
		num(meta["quota"].(map[string]any)["limit"]) != 200 || num(meta["quota"].(map[string]any)["used"]) != 0 {
		t.Fatalf("管理页异常: %v", meta)
	}
	if status, _ := e.as(t, reader, http.MethodGet, base, ""); status != http.StatusForbidden {
		t.Fatalf("非作者应被拒绝: %d", status)
	}

	// 事件流：生成时推送章节状态
	r, _ := http.NewRequest(http.MethodGet, e.server.URL+base+"/stream", nil)
	r.Header.Set("Authorization", "Bearer "+tokenOf(e, author))
	streamClient := &http.Client{}
	resp, err := streamClient.Do(r)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("订阅失败: %v", err)
	}
	events := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		name := ""
		for sc.Scan() {
			line := sc.Text()
			if n, ok := strings.CutPrefix(line, "event: "); ok {
				name = n
			} else if d, ok := strings.CutPrefix(line, "data: "); ok && name == "guide" {
				var g map[string]any
				_ = json.Unmarshal([]byte(d), &g)
				events <- fmt.Sprint(g["status"])
			}
		}
	}()
	t.Cleanup(func() { resp.Body.Close() })
	time.Sleep(100 * time.Millisecond)

	// 批量生成：跳过空章节，草稿也可生成
	status, p := e.as(t, author, http.MethodPost, base+"/generate", `{"scope":"missing"}`)
	if status != http.StatusOK || num(data(p)["queued"]) != 3 {
		t.Fatalf("批量生成失败: %d %v", status, p)
	}
	e.runJobs(t)
	rows, meta = e.chapters(t, author, bookID)
	g1 := rows[ch1]
	if g1["status"] != "ready" || !strings.HasSuffix(g1["summary"].(string), "：缓存") || len(g1["points"].([]any)) != 2 || rows[empty] != nil || fake.count() != 3 {
		t.Fatalf("生成结果异常: %v / 空章节 %v / 调用 %d", g1, rows[empty], fake.count())
	}
	if num(meta["quota"].(map[string]any)["used"]) != 3 {
		t.Fatalf("本月次数应为 3: %v", meta["quota"])
	}
	seen := map[string]bool{}
	for len(events) > 0 {
		seen[<-events] = true
	}
	if !seen["queued"] || !seen["generating"] || !seen["ready"] {
		t.Fatalf("事件流应推送排队、生成中、完成: %v", seen)
	}
	var usage models.AIUsageLog
	if e.db.Where("feature = ? AND ref_id = ?", "chapterguide.chapter", ch1).First(&usage).Error != nil || usage.UserID != author.ID || usage.RefType != "document" {
		t.Fatalf("用量应记在作者名下: %+v", usage)
	}

	// 内容未变：没有需要生成的章节，不重复调用
	if status, p := e.as(t, author, http.MethodPost, base+"/generate", `{"scope":"missing"}`); status != http.StatusBadRequest {
		t.Fatalf("内容未变不应重新生成: %d %v", status, p)
	}

	// 读者：已发布章节可见；草稿不可见（作者可见）
	_, p = e.req(t, "", http.MethodGet, fmt.Sprintf("/api/v1/chapter-guides/docs/%d", ch1), "")
	if g := data(p)["guide"].(map[string]any); !strings.HasSuffix(g["summary"].(string), "：缓存") {
		t.Fatalf("读者应看到导读: %v", p)
	}
	if _, p := e.req(t, "", http.MethodGet, fmt.Sprintf("/api/v1/chapter-guides/docs/%d", ch3), ""); data(p)["guide"] != nil {
		t.Fatalf("草稿章节的导读不应对读者可见: %v", p)
	}
	if _, p := e.as(t, author, http.MethodGet, fmt.Sprintf("/api/v1/chapter-guides/docs/%d", ch3), ""); data(p)["guide"] == nil {
		t.Fatalf("作者应能看到草稿章节的导读")
	}

	// 修改正文：导读过期，批量生成只处理该章
	e.as(t, author, http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", ch1), `{"content":"缓存可以加速读取，也会带来一致性问题。"}`)
	rows, _ = e.chapters(t, author, bookID)
	if rows[ch1]["stale"] != true || rows[ch2]["stale"] != false {
		t.Fatalf("修改后应显示过期: %v", rows[ch1])
	}
	if _, p := e.as(t, author, http.MethodPost, base+"/generate", `{"scope":"missing"}`); num(data(p)["queued"]) != 1 {
		t.Fatalf("应只重新生成过期的一章: %v", p)
	}
	e.runJobs(t)
	if rows, _ = e.chapters(t, author, bookID); rows[ch1]["stale"] != false || fake.count() != 4 {
		t.Fatalf("重新生成后不应过期: %v", rows[ch1])
	}

	// 作者编辑的导读不被批量生成覆盖；强制重新生成才会替换
	if status, p := e.as(t, author, http.MethodPut, fmt.Sprintf("/api/v1/chapter-guides/docs/%d", ch2), `{"summary":"作者写的导读","points":["一","",  "二"]}`); status != http.StatusOK || data(p)["edited"] != true || len(data(p)["points"].([]any)) != 2 {
		t.Fatalf("编辑导读失败: %d %v", status, p)
	}
	e.as(t, author, http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", ch2), `{"content":"索引帮助检索，B+ 树最常见。"}`)
	if status, _ := e.as(t, author, http.MethodPost, base+"/generate", `{"scope":"missing"}`); status != http.StatusBadRequest {
		t.Fatalf("编辑过的导读不应被批量覆盖: %d", status)
	}
	if _, p := e.as(t, author, http.MethodPost, base+"/generate", fmt.Sprintf(`{"scope":"ids","doc_ids":[%d],"force":true}`, ch2)); num(data(p)["queued"]) != 1 {
		t.Fatalf("强制重新生成失败: %v", p)
	}
	e.runJobs(t)
	if rows, _ = e.chapters(t, author, bookID); rows[ch2]["edited"] != false || rows[ch2]["summary"] == "作者写的导读" {
		t.Fatalf("强制重新生成应替换作者编辑的导读: %v", rows[ch2])
	}

	// 全书概览：基于已发布章节的导读，读者可见
	if status, p := e.as(t, author, http.MethodPost, base+"/overview/generate", `{}`); status != http.StatusOK {
		t.Fatalf("生成概览失败: %d %v", status, p)
	}
	e.runJobs(t)
	if prompt := fake.last(); !strings.Contains(prompt, "### 缓存") || !strings.Contains(prompt, rows[ch1]["summary"].(string)) || strings.Contains(prompt, "草稿章") {
		t.Fatalf("概览素材异常: %s", prompt)
	}
	if _, p := e.req(t, "", http.MethodGet, base+"/overview", ""); !strings.Contains(data(p)["overview"].(map[string]any)["content"].(string), "这本书讲存储") {
		t.Fatalf("读者应看到概览: %v", p)
	}
	if status, p := e.as(t, author, http.MethodPut, base+"/overview", `{"content":"作者改写的概览"}`); status != http.StatusOK || data(p)["overview"].(map[string]any)["edited"] != true {
		t.Fatalf("编辑概览失败: %d %v", status, p)
	}

	// 费用由站点承担：用量记为系统调用，次数仍计入作者
	if status, p := e.req(t, e.token, http.MethodPut, "/api/v1/admin/chapter-guides/settings", `{"cost_bearer":"site"}`); status != http.StatusOK {
		t.Fatalf("设置费用承担方失败: %d %v", status, p)
	}
	e.as(t, author, http.MethodPost, base+"/generate", fmt.Sprintf(`{"scope":"ids","doc_ids":[%d],"force":true}`, ch1))
	e.runJobs(t)
	var last models.AIUsageLog
	e.db.Where("feature = ?", "chapterguide.chapter").Order("id DESC").First(&last)
	if last.UserID != 0 {
		t.Fatalf("站点承担时应记为系统调用: %+v", last)
	}
	var runs int64
	e.db.Model(&chapterguide.Run{}).Where("user_id = ?", author.ID).Count(&runs)
	if runs != 7 {
		t.Fatalf("生成次数应为 7: %d", runs)
	}

	// 每月次数为权益：不足时拒绝
	e.req(t, e.token, http.MethodPut, "/api/v1/admin/entitlements/base", `{"values":{"chapterguide.monthly":8}}`)
	if status, p := e.as(t, author, http.MethodPost, base+"/generate", fmt.Sprintf(`{"scope":"ids","doc_ids":[%d,%d],"force":true}`, ch1, ch2)); status != http.StatusTooManyRequests || !strings.Contains(p["message"].(string), "剩余 1 次") {
		t.Fatalf("次数不足应被拒绝: %d %v", status, p)
	}
	e.req(t, e.token, http.MethodPut, "/api/v1/admin/entitlements/base", `{"values":{"chapterguide.monthly":-1}}`)

	// 自动模式：首次发布立即生成；已发布章节修改后标记待更新，由延迟触发或巡检生成
	e.as(t, author, http.MethodPut, base+"/settings", `{"auto_generate":true}`)
	ch5 := e.doc(t, author, bookID, `{"title":"日志","content":"预写日志保证持久性。","status":"published","sort_order":4}`)
	e.runJobs(t)
	if rows, _ = e.chapters(t, author, bookID); rows[ch5]["status"] != "ready" {
		t.Fatalf("自动模式下发布后应生成导读: %v", rows[ch5])
	}
	e.as(t, author, http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", ch5), `{"content":"预写日志保证持久性与崩溃恢复。"}`)
	var dirty chapterguide.Guide
	e.db.Where("doc_id = ?", ch5).First(&dirty)
	if dirty.DirtyAt == nil {
		t.Fatalf("修改后应标记待更新")
	}
	e.db.Model(&dirty).Update("dirty_at", time.Now().Add(-10*time.Minute))
	before := fake.count()
	plugincore.FireJobQueueSweep(e.app, e.app.Jobs)
	e.runJobs(t)
	if rows, _ = e.chapters(t, author, bookID); rows[ch5]["stale"] != false || fake.count() != before+1 {
		t.Fatalf("巡检应生成待更新的导读: %v", rows[ch5])
	}

	// 删除导读；插件关闭后接口不可用
	if status, _ := e.as(t, author, http.MethodDelete, fmt.Sprintf("/api/v1/chapter-guides/docs/%d", ch3), ""); status != http.StatusOK {
		t.Fatalf("删除导读失败: %d", status)
	}
	e.req(t, e.token, http.MethodPost, "/api/v1/admin/plugins/chapter-guide/uninstall", "")
	if status, _ := e.req(t, "", http.MethodGet, fmt.Sprintf("/api/v1/chapter-guides/docs/%d", ch1), ""); status != http.StatusNotFound {
		t.Fatalf("插件关闭后应返回 404: %d", status)
	}
}
