package booktranslations_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"knowforge/server/internal/app"
	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugins/booktranslations"
	"knowforge/server/internal/testdb"
)

// 集成测试（经 HTTP，AI 服务用本地假服务器模拟 OpenAI 兼容接口）：整本翻译（目录、结构、代码块、术语表、计量）、同步、失败重试、暂停继续、额度与权限。

type fakeAI struct {
	mu      sync.Mutex
	systems []string
	inputs  []string
	delay   time.Duration
	failOn  string // 原文含此文字时返回 500
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
		system, input := "", ""
		for _, m := range req.Messages {
			if m.Role == "system" {
				system = m.Content
			}
			if m.Role == "user" {
				input = m.Content
			}
		}
		f.mu.Lock()
		f.systems = append(f.systems, system)
		f.inputs = append(f.inputs, input)
		delay, failOn := f.delay, f.failOn
		f.mu.Unlock()
		if failOn != "" && strings.Contains(input, failOn) {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		out := "EN:" + input
		if strings.Contains(input, "丢占位") { // 模拟模型丢掉占位符
			out = regexp.MustCompile(`⟦\d+⟧`).ReplaceAllString(out, "")
		}
		if strings.Contains(system, "JSON 字符串数组") {
			var list []string
			_ = json.Unmarshal([]byte(input), &list)
			for i := range list {
				list[i] = "EN:" + list[i]
			}
			b, _ := json.Marshal(list)
			out = string(b)
		}
		if !req.Stream {
			_ = json.NewEncoder(w).Encode(map[string]any{"model": "fake-1", "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
				"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": out}}}})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(v any) {
			b, _ := json.Marshal(v)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
			w.(http.Flusher).Flush()
		}
		runes := []rune(out)
		half := len(runes) / 2
		write(map[string]any{"model": "fake-1", "choices": []map[string]any{{"delta": map[string]any{"content": string(runes[:half])}}}})
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		write(map[string]any{"choices": []map[string]any{{"delta": map[string]any{"content": string(runes[half:])}}}})
		write(map[string]any{"choices": []any{}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5}})
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	})
	return mux
}

func (f *fakeAI) set(delay time.Duration, failOn string) {
	f.mu.Lock()
	f.delay, f.failOn = delay, failOn
	f.mu.Unlock()
}

func (f *fakeAI) inputsContaining(text string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, in := range f.inputs {
		if strings.Contains(in, text) {
			out = append(out, in)
		}
	}
	return out
}

func (f *fakeAI) seen(text string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, in := range f.inputs {
		if strings.Contains(in, text) {
			return true
		}
	}
	return false
}

func (f *fakeAI) lastSystem() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.systems[len(f.systems)-1]
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
	status, installed := e.req(t, "", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"翻译测试"},"admin":{"username":"t-admin","email":"t-admin@test.local","password":"secret123"}}`)
	if status != http.StatusOK {
		t.Fatalf("安装失败: %d %v", status, installed)
	}
	e.token = installed["data"].(map[string]any)["token"].(string)
	e.db = a.DB
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

func num(v any) int { f, _ := v.(float64); return int(f) }

func (e *testEnv) doc(t *testing.T, u *models.User, bookID uint, body string) models.Document {
	t.Helper()
	status, p := e.as(t, u, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), body)
	if status != http.StatusOK {
		t.Fatalf("创建章节失败: %d %v", status, p)
	}
	var d models.Document
	e.db.First(&d, uint(num(data(p)["id"])))
	return d
}

// wait 等待任务离开进行中状态，返回 {job, items}。
func (e *testEnv) wait(t *testing.T, u *models.User, jobID int) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, p := e.as(t, u, http.MethodGet, fmt.Sprintf("/api/v1/ai-translate/jobs/%d", jobID), "")
		if data(p)["job"].(map[string]any)["status"] != "running" {
			return data(p)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("任务 %d 未在测试时限内结束", jobID)
	return nil
}

func jobOf(v map[string]any) map[string]any { return v["job"].(map[string]any) }

func TestAIBookTranslation(t *testing.T) {
	fake := &fakeAI{}
	aiServer := httptest.NewServer(fake.handler())
	t.Cleanup(aiServer.Close)
	e := newTestEnv(t, aiServer.URL)
	author, other := e.user(t, "author"), e.user(t, "other")

	_, created := e.as(t, author, http.MethodPost, "/api/v1/books", `{"title":"存储原理","description":"讲解存储","status":"published","is_public":true}`)
	bookID := uint(num(data(created)["id"]))
	base := fmt.Sprintf("/api/v1/books/%d/ai-translate", bookID)
	ch1 := e.doc(t, author, bookID, fmt.Sprintf(`{"title":"第一章","slug":"intro","content":%q,"status":"published"}`, "---\ntitle: 缓存\nurl: https://example.com/cache\n---\n<!-- icon: bolt -->\n\n缓存可以加速读取。\n\n```go\n// 注释\nfunc main() {}\n```\n\n第二段内容。\n\n[children]\n\n<Card title=\"卡片\" icon=\"book\">\n卡片正文\n</Card>\n\n见 [[第二章]]。"))
	ch2 := e.doc(t, author, bookID, `{"title":"第二章","slug":"index","content":"索引。","status":"draft","sort_order":1}`)
	e.doc(t, author, bookID, fmt.Sprintf(`{"title":"第二章第一节","slug":"btree","content":"B+ 树。","status":"draft","parent_id":%d}`, ch2.ID))

	// 概况
	status, ov := e.as(t, author, http.MethodGet, base, "")
	if status != http.StatusOK || data(ov)["available"] != true || data(ov)["allowed"] != true || num(data(ov)["chars_left"]) != -1 || num(data(ov)["source"].(map[string]any)["chapters"]) != 3 {
		t.Fatalf("概况异常: %d %v", status, ov)
	}
	if status, _ := e.as(t, other, http.MethodGet, base, ""); status != http.StatusForbidden {
		t.Fatalf("非作者应被拒绝: %d", status)
	}

	// 术语表
	if status, p := e.as(t, author, http.MethodPut, base+"/glossary", `{"lang":"en","terms":[{"source":"缓存","target":""}]}`); status != http.StatusBadRequest {
		t.Fatalf("术语缺少译文应被拒绝: %d %v", status, p)
	}
	if status, p := e.as(t, author, http.MethodPut, base+"/glossary", `{"lang":"en","terms":[{"source":"缓存","target":"cache"},{"source":"","target":""}]}`); status != http.StatusOK || len(data(p)["terms"].([]any)) != 1 {
		t.Fatalf("保存术语表失败: %d %v", status, p)
	}

	// 新建译本：先翻译目录建章节，再逐章翻译；代码块原样保留且不发给模型
	status, p := e.as(t, author, http.MethodPost, base+"/jobs", `{"target_lang":"en","target_label":"English","instructions":"语气正式"}`)
	if status != http.StatusOK || jobOf(data(p))["status"] != "running" || num(jobOf(data(p))["total"]) != 3 {
		t.Fatalf("创建任务失败: %d %v", status, p)
	}
	jobID := num(jobOf(data(p))["id"])
	done := e.wait(t, author, jobID)
	job := jobOf(done)
	if job["status"] != "done" || num(job["done"]) != 3 || num(job["failed"]) != 0 || num(job["chars"]) == 0 || num(job["input_tokens"]) == 0 {
		t.Fatalf("任务结果异常: %v", job)
	}
	targetID := uint(num(job["target_book_id"]))
	var src, dst models.Book
	e.db.First(&src, bookID)
	e.db.First(&dst, targetID)
	if dst.Title != "EN:存储原理" || dst.Description != "EN:讲解存储" || dst.Language != "English" || dst.Status != "draft" || dst.IsPublic || dst.TransGroup == "" || dst.TransGroup != src.TransGroup || dst.UserID != author.ID {
		t.Fatalf("译本书籍异常: %+v / 原书分组 %q", dst, src.TransGroup)
	}
	var docs []models.Document
	e.db.Where("book_id = ?", targetID).Order("id ASC").Find(&docs)
	if len(docs) != 3 {
		t.Fatalf("译本章节数异常: %d", len(docs))
	}
	bySlug := map[string]models.Document{}
	for _, d := range docs {
		bySlug[d.Slug] = d
		if d.Status != "draft" {
			t.Fatalf("译稿应为草稿: %+v", d)
		}
	}
	intro, index, btree := bySlug["intro"], bySlug["index"], bySlug["btree"]
	if intro.Title != "EN:第一章" || btree.ParentID == nil || *btree.ParentID != index.ID || index.SortOrder != 1 {
		t.Fatalf("译本结构异常: %+v", bySlug)
	}
	if !strings.Contains(intro.Content, "EN:缓存可以加速读取。") || !strings.Contains(intro.Content, "```go\n// 注释\nfunc main() {}\n```") || !strings.Contains(intro.Content, "第二段内容。") {
		t.Fatalf("译文异常: %q", intro.Content)
	}
	// 扩展语法原样保留；双向链接改写为 slug 形式，显示文字参与翻译
	if !strings.HasPrefix(intro.Content, "---\ntitle: EN:缓存\nurl: https://example.com/cache\n---\n<!-- icon: bolt -->\n") || intro.Icon != "bolt" {
		t.Fatalf("front-matter 应保持结构、只翻译 title: %q (icon %q)", intro.Content, intro.Icon)
	}
	for _, want := range []string{"\n[children]\n", "<Card title=\"卡片\" icon=\"book\">", "</Card>", "[[index|第二章]]"} {
		if !strings.Contains(intro.Content, want) {
			t.Fatalf("译文应保留 %q: %q", want, intro.Content)
		}
	}
	for _, hidden := range []string{"func main", "children", "Card", "icon", "[[", "url:", "https://example.com/cache"} {
		if fake.seen(hidden) {
			t.Fatalf("%q 不应发给模型", hidden)
		}
	}
	if sys := fake.lastSystem(); !strings.Contains(sys, "⟦数字⟧") {
		t.Fatalf("系统提示应说明占位符: %s", sys)
	}
	if sys := fake.lastSystem(); !strings.Contains(sys, "缓存 → cache") || !strings.Contains(sys, "语气正式") || !strings.Contains(sys, "English") {
		t.Fatalf("系统提示缺少术语表或要求: %s", sys)
	}
	var revs int64
	e.db.Model(&models.DocumentRevision{}).Where("document_id = ? AND reason = ?", intro.ID, "ai_translate").Count(&revs)
	if revs != 1 {
		t.Fatalf("应记录一条翻译版本: %d", revs)
	}
	// 计量：每次调用记为 translate.book，原文字符计入本月翻译字数
	var logs []models.AIUsageLog
	e.db.Where("feature = ? AND user_id = ?", "translate.book", author.ID).Find(&logs)
	var logged int64
	for _, l := range logs {
		logged += l.Characters
	}
	if len(logs) == 0 || logged != int64(num(job["chars"])) {
		t.Fatalf("用量记录异常: %d 条, %d 字 / 任务 %v 字", len(logs), logged, job["chars"])
	}
	if _, u := e.as(t, author, http.MethodGet, "/api/v1/users/me/ai-usage", ""); int64(num(data(u)["translate_chars"])) != logged {
		t.Fatalf("本月翻译字数应计入整本翻译: %v", data(u)["translate_chars"])
	}
	var note models.Notification
	if e.db.Where("user_id = ?", author.ID).Order("id DESC").First(&note).Error != nil || !strings.Contains(note.Title, "EN:存储原理") {
		t.Fatalf("完成后应以译本书名通知作者: %+v", note)
	}

	// 已有译本：无待同步章节
	_, ov = e.as(t, author, http.MethodGet, base, "")
	targets := data(ov)["targets"].([]any)
	if len(targets) != 1 || num(targets[0].(map[string]any)["changed"]) != 0 || num(targets[0].(map[string]any)["added"]) != 0 {
		t.Fatalf("译本概况异常: %v", targets)
	}

	// 原文修改一章、新增一章 → 同步只翻译这两章
	e.as(t, author, http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", ch1.ID), `{"title":"第一章 缓存","content":"新的内容。"}`)
	e.doc(t, author, bookID, `{"title":"第三章","slug":"wal","content":"日志。","status":"draft","sort_order":2}`)
	_, ov = e.as(t, author, http.MethodGet, base, "")
	tv := data(ov)["targets"].([]any)[0].(map[string]any)
	if num(tv["changed"]) != 1 || num(tv["added"]) != 1 {
		t.Fatalf("待同步章节异常: %v", tv)
	}
	status, p = e.as(t, author, http.MethodPost, base+"/jobs", fmt.Sprintf(`{"target_book_id":%d}`, targetID))
	if status != http.StatusOK || num(jobOf(data(p))["total"]) != 2 || jobOf(data(p))["mode"] != "sync" {
		t.Fatalf("同步任务异常: %d %v", status, p)
	}
	if done := e.wait(t, author, num(jobOf(data(p))["id"])); jobOf(done)["status"] != "done" || num(jobOf(done)["done"]) != 2 {
		t.Fatalf("同步结果异常: %v", jobOf(done))
	}
	e.db.First(&intro, intro.ID)
	var wal models.Document
	if intro.Content != "EN:新的内容。\n" || intro.Title != "EN:第一章 缓存" || e.db.Where("book_id = ? AND slug = ?", targetID, "wal").First(&wal).Error != nil {
		t.Fatalf("同步后译文异常: %+v", intro)
	}
	if status, p := e.as(t, author, http.MethodPost, base+"/jobs", fmt.Sprintf(`{"target_book_id":%d}`, targetID)); status != http.StatusBadRequest || !strings.Contains(p["message"].(string), "已是最新") {
		t.Fatalf("没有变化时应拒绝同步: %d %v", status, p)
	}

	// 某章翻译失败：其余章节继续，失败章可重试
	fake.set(0, "坏掉")
	e.doc(t, author, bookID, `{"title":"第四章","slug":"broken","content":"坏掉的章节。","status":"draft","sort_order":3}`)
	_, p = e.as(t, author, http.MethodPost, base+"/jobs", fmt.Sprintf(`{"target_book_id":%d}`, targetID))
	failedJob := num(jobOf(data(p))["id"])
	if done := e.wait(t, author, failedJob); jobOf(done)["status"] != "done" || num(jobOf(done)["failed"]) != 1 {
		t.Fatalf("失败章节应记为失败: %v", jobOf(done))
	}
	fake.set(0, "")
	if status, p := e.as(t, author, http.MethodPost, fmt.Sprintf("/api/v1/ai-translate/jobs/%d/retry", failedJob), ""); status != http.StatusOK {
		t.Fatalf("重试失败: %d %v", status, p)
	}
	if done := e.wait(t, author, failedJob); jobOf(done)["status"] != "done" || num(jobOf(done)["failed"]) != 0 || num(jobOf(done)["done"]) != 1 {
		t.Fatalf("重试结果异常: %v", jobOf(done))
	}

	// 模型丢失占位符（格式/组件语法）：重试一次，仍不行则该处保留原文、章节照常完成并说明，不保存损坏的译文
	lossy := e.doc(t, author, bookID, `{"title":"第五章","slug":"lossy","content":"丢占位的章节 [children]。\n\n正常的段落。","status":"draft","sort_order":4}`)
	_, p = e.as(t, author, http.MethodPost, base+"/jobs", fmt.Sprintf(`{"target_book_id":%d}`, targetID))
	lossyJob := num(jobOf(data(p))["id"])
	lossyDone := e.wait(t, author, lossyJob)
	if jobOf(lossyDone)["status"] != "done" || num(jobOf(lossyDone)["failed"]) != 0 || num(jobOf(lossyDone)["done"]) != 1 {
		t.Fatalf("无法保留格式的段落不应让整章失败: %v", jobOf(lossyDone))
	}
	if item := lossyDone["items"].([]any)[0].(map[string]any); !strings.Contains(item["error"].(string), "已保留原文") {
		t.Fatalf("应说明保留了原文: %v", item)
	}
	var lossyDst models.Document
	e.db.Where("book_id = ? AND slug = ?", targetID, "lossy").First(&lossyDst)
	if !strings.Contains(lossyDst.Content, "丢占位的章节 [children]。") || !strings.Contains(lossyDst.Content, "EN:正常的段落。") {
		t.Fatalf("无法保留格式的段落保留原文、其余段落照常翻译: %q", lossyDst.Content)
	}
	e.db.Delete(&models.Document{}, lossy.ID)

	// 暂停与继续；事件流先推 snapshot，结束推 done
	fake.set(400*time.Millisecond, "")
	_, p = e.as(t, author, http.MethodPost, base+"/jobs", `{"target_lang":"ja","target_label":"日本語","title":"ストレージ"}`)
	jaJob := num(jobOf(data(p))["id"])
	time.Sleep(200 * time.Millisecond)
	if status, p := e.as(t, other, http.MethodPost, fmt.Sprintf("/api/v1/ai-translate/jobs/%d/pause", jaJob), ""); status != http.StatusNotFound {
		t.Fatalf("不能暂停他人的任务: %d %v", status, p)
	}
	if status, p := e.as(t, author, http.MethodPost, fmt.Sprintf("/api/v1/ai-translate/jobs/%d/pause", jaJob), ""); status != http.StatusOK {
		t.Fatalf("暂停失败: %d %v", status, p)
	}
	paused := e.wait(t, author, jaJob)
	if jobOf(paused)["status"] != "paused" {
		t.Fatalf("应已暂停: %v", jobOf(paused))
	}
	for _, it := range paused["items"].([]any) {
		if it.(map[string]any)["status"] == "running" {
			t.Fatalf("暂停后不应有进行中的章节: %v", it)
		}
	}
	fake.set(0, "")
	if status, p := e.as(t, author, http.MethodPost, fmt.Sprintf("/api/v1/ai-translate/jobs/%d/resume", jaJob), ""); status != http.StatusOK {
		t.Fatalf("继续失败: %d %v", status, p)
	}
	r, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/v1/ai-translate/jobs/%d/stream", e.server.URL, jaJob), nil)
	r.Header.Set("Authorization", "Bearer "+tokenOf(e, author))
	resp, err := e.client.Do(r)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("订阅失败: %v", err)
	}
	events := []string{}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if name, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
			events = append(events, name)
			if name == "done" {
				break
			}
		}
	}
	resp.Body.Close()
	if events[0] != "snapshot" || events[len(events)-1] != "done" {
		t.Fatalf("事件流异常: %v", events)
	}
	jaDone := e.wait(t, author, jaJob)
	var ja models.Book
	e.db.First(&ja, uint(num(jobOf(jaDone)["target_book_id"])))
	if jobOf(jaDone)["status"] != "done" || num(jobOf(jaDone)["done"]) != 5 || ja.Title != "ストレージ" {
		t.Fatalf("继续后结果异常: %v %q", jobOf(jaDone), ja.Title)
	}

	// 额度：每月翻译字数不足时拒绝；整本 AI 翻译权益关闭时不可用
	if status, p := e.req(t, e.token, http.MethodPut, "/api/v1/admin/entitlements/base", `{"values":{"translate.monthly_chars":10}}`); status != http.StatusOK {
		t.Fatalf("设置翻译字数失败: %d %v", status, p)
	}
	if status, p := e.as(t, author, http.MethodPost, base+"/jobs", `{"target_lang":"fr","target_label":"Français"}`); status != http.StatusTooManyRequests || !strings.Contains(p["message"].(string), "本月翻译字数不足") {
		t.Fatalf("字数不足应被拒绝: %d %v", status, p)
	}
	e.req(t, e.token, http.MethodPut, "/api/v1/admin/entitlements/base", `{"values":{"translate.monthly_chars":-1,"translate.ai_book":0}}`)
	if status, p := e.as(t, author, http.MethodPost, base+"/jobs", `{"target_lang":"fr","target_label":"Français"}`); status != http.StatusForbidden {
		t.Fatalf("权益关闭时应不可用: %d %v", status, p)
	}
	var items int64
	e.db.Model(&booktranslations.TranslateItem{}).Count(&items)
	if items != 3+2+1+1+5 { // 新建、同步、失败章、丢失占位符的章、日文译本
		t.Fatalf("任务章节数异常: %d", items)
	}
}
