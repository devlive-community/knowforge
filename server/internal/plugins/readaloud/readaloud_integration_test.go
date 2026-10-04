package readaloud_test

import (
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
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/testdb"
)

// 集成测试（经 HTTP，语音合成用本地假服务器模拟）：可用状态、只朗读出自本章且有权阅读的文字、缓存复用、
// 每月字数权益（同段重复收听不计）、AI 用量记录、管理员清空缓存与插件开关。

type fakeTTS struct {
	mu     sync.Mutex
	inputs []string
}

func (f *fakeTTS) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/audio/speech", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input string `json:"input"`
			Voice string `json:"voice"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &req)
		f.mu.Lock()
		f.inputs = append(f.inputs, req.Voice+":"+req.Input)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("ID3:" + req.Voice + ":" + req.Input))
	})
	return mux
}

func (f *fakeTTS) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.inputs)
}

type testEnv struct {
	app    *app.App
	db     *gorm.DB
	token  string
	server *httptest.Server
}

func newTestEnv(t *testing.T, ttsURL string) *testEnv {
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
	e := &testEnv{app: a, server: httptest.NewServer(a.Router())}
	t.Cleanup(e.server.Close)
	status, installed := e.json(t, "", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"朗读测试"},"admin":{"username":"ra-admin","email":"ra-admin@test.local","password":"secret123"}}`)
	if status != http.StatusOK {
		t.Fatalf("安装失败: %d %v", status, installed)
	}
	e.token = installed["data"].(map[string]any)["token"].(string)
	e.db = a.DB
	if status, p := e.json(t, e.token, http.MethodPost, "/api/v1/admin/plugins/read-aloud/install", ""); status != http.StatusOK {
		t.Fatalf("启用插件失败: %d %v", status, p)
	}
	body := fmt.Sprintf(`{"provider":"openai","base_url":%q,"api_key":"test-key","model":"fake","tts_model":"tts-fake","tts_voices":"nova, onyx","price_tts":"15"}`, ttsURL+"/v1")
	if status, p := e.json(t, e.token, http.MethodPut, "/api/v1/admin/ai", body); status != http.StatusOK {
		t.Fatalf("配置 AI 服务失败: %d %v", status, p)
	}
	return e
}

func (e *testEnv) do(t *testing.T, token, method, path, body string) *http.Response {
	t.Helper()
	r, _ := http.NewRequest(method, e.server.URL+path, bytes.NewReader([]byte(body)))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func (e *testEnv) json(t *testing.T, token, method, path, body string) (int, map[string]any) {
	t.Helper()
	resp := e.do(t, token, method, path, body)
	defer resp.Body.Close()
	p := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&p)
	return resp.StatusCode, p
}

func (e *testEnv) user(t *testing.T, name string) (*models.User, string) {
	t.Helper()
	u := &models.User{Username: name, Email: name + "@test.local", Role: "user", IsActive: true, EmailVerified: true}
	if err := e.db.Create(u).Error; err != nil {
		t.Fatal(err)
	}
	token, _ := auth.GenerateToken(e.app.Config.Secret, u.ID, u.Username, u.Role)
	return u, token
}

// speak 请求一段朗读，返回状态码、音频（成功时）或错误信息，以及计入后的本月用量。
func (e *testEnv) speak(t *testing.T, token string, docID uint, text, voice string) (int, string, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"text": text, "voice": voice})
	resp := e.do(t, token, http.MethodPost, fmt.Sprintf("/api/v1/read-aloud/docs/%d/speech", docID), string(body))
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		msg, _ := p["message"].(string)
		return resp.StatusCode, msg, ""
	}
	if ct := resp.Header.Get("Content-Type"); ct != "audio/mpeg" {
		t.Fatalf("音频类型不对: %s", ct)
	}
	return resp.StatusCode, string(raw), resp.Header.Get("X-Quota-Used")
}

func data(p map[string]any) map[string]any { d, _ := p["data"].(map[string]any); return d }

func TestReadAloud(t *testing.T) {
	tts := &fakeTTS{}
	ttsServer := httptest.NewServer(tts.handler())
	defer ttsServer.Close()
	e := newTestEnv(t, ttsServer.URL)

	// 付费章节：用测试门禁模拟（标题含「付费」的章节读者未解锁）
	plugincore.RegisterContentGate(plugincore.ContentGate{Document: func(_ plugincore.Core, _ *models.User, _ *models.Book, doc *models.Document) plugincore.ContentAccess {
		return plugincore.ContentAccess{Allowed: !strings.Contains(doc.Title, "付费")}
	}})

	author, authorToken := e.user(t, "ra-author")
	_, readerToken := e.user(t, "ra-reader")
	book := models.Book{Title: "朗读书", Slug: "ra-book", UserID: author.ID, Status: "published", IsPublic: true}
	e.db.Create(&book)
	doc := models.Document{BookID: book.ID, Title: "第一章", Slug: "one", Status: "published", Content: "## 开篇\n\n第一段**加粗**的文字。\n\n- 列表 [链接](https://x.test) 项\n\nHello, World!"}
	draft := models.Document{BookID: book.ID, Title: "草稿", Slug: "draft", Status: "draft", Content: "草稿正文"}
	paid := models.Document{BookID: book.ID, Title: "付费章", Slug: "paid", Status: "published", Content: "付费正文"}
	e.db.Create(&doc)
	e.db.Create(&draft)
	e.db.Create(&paid)

	status, p := e.json(t, readerToken, http.MethodGet, "/api/v1/read-aloud/status", "")
	if d := data(p); status != http.StatusOK || d["available"] != true || d["default_voice"] != "nova" || d["used"].(float64) != 0 || d["limit"].(float64) != 20000 {
		t.Fatalf("状态不对: %d %v", status, p)
	}

	// 排版后的文字（去掉 Markdown 标记、空白不同）可以朗读；未知音色回退为默认音色
	code, audio, used := e.speak(t, readerToken, doc.ID, "第一段加粗的文字。", "robot")
	if code != http.StatusOK || audio != "ID3:nova:第一段加粗的文字。" || used != "9" {
		t.Fatalf("朗读失败: %d %s used=%s", code, audio, used)
	}
	if code, _, _ := e.speak(t, readerToken, doc.ID, "列表 链接 项", "onyx"); code != http.StatusOK {
		t.Fatalf("列表项应可朗读: %d", code)
	}
	if code, _, _ := e.speak(t, readerToken, doc.ID, "第一章", "nova"); code != http.StatusOK {
		t.Fatalf("章节标题应可朗读: %d", code)
	}
	// 相同文字与音色复用缓存，且当月重复收听不再计数
	before := tts.count()
	code, audio, used = e.speak(t, readerToken, doc.ID, "第一段加粗的文字。", "nova")
	if code != http.StatusOK || tts.count() != before || audio != "ID3:nova:第一段加粗的文字。" || used != "19" {
		t.Fatalf("应命中缓存且不重复计数: %d calls=%d used=%s", code, tts.count()-before, used)
	}

	// 不属于本章的文字、草稿（读者）、未解锁的付费章节都拒绝
	if code, msg, _ := e.speak(t, readerToken, doc.ID, "随便一段别的话", "nova"); code != http.StatusBadRequest || !strings.Contains(msg, "不属于本章") {
		t.Fatalf("非本章文字应拒绝: %d %s", code, msg)
	}
	if code, _, _ := e.speak(t, readerToken, draft.ID, "草稿正文", "nova"); code != http.StatusNotFound {
		t.Fatalf("读者不能收听草稿: %d", code)
	}
	if code, _, _ := e.speak(t, authorToken, draft.ID, "草稿正文", "nova"); code != http.StatusOK {
		t.Fatalf("作者可以收听草稿: %d", code)
	}
	if code, msg, _ := e.speak(t, readerToken, paid.ID, "付费正文", "nova"); code != http.StatusForbidden || !strings.Contains(msg, "解锁") {
		t.Fatalf("未解锁的付费章节应拒绝: %d %s", code, msg)
	}

	// 用量记录：按字数记入 AI 用量，费用按每百万字符单价估算
	var logs []models.AIUsageLog
	e.db.Where("feature = ?", "readaloud.speech").Order("id").Find(&logs)
	if len(logs) != 4 || logs[0].Kind != "tts" || logs[0].Characters != 9 || logs[0].Model != "tts-fake" || logs[0].CostMicros != 135 || logs[0].RefType != "document" || logs[0].RefID != doc.ID {
		t.Fatalf("用量记录不对: %+v", logs)
	}

	// 每月字数权益：超出后新的段落被拒绝，已听过的段落仍可重放
	_ = e.app.SetSetting("entitlement_readaloud_monthly_chars", "20", "test")
	if code, msg, _ := e.speak(t, readerToken, doc.ID, "Hello, World!", "nova"); code != http.StatusTooManyRequests || !strings.Contains(msg, "本月朗读字数已用完（20 字）") {
		t.Fatalf("超出额度应拒绝: %d %s", code, msg)
	}
	if code, _, _ := e.speak(t, readerToken, doc.ID, "第一段加粗的文字。", "nova"); code != http.StatusOK {
		t.Fatalf("已听过的段落应可重放: %d", code)
	}
	_, p = e.json(t, readerToken, http.MethodGet, "/api/v1/read-aloud/status", "")
	if d := data(p); d["used"].(float64) != 19 || d["limit"].(float64) != 20 {
		t.Fatalf("本月用量不对: %v", d)
	}

	// 管理员：查看并清空缓存
	_, p = e.json(t, e.token, http.MethodGet, "/api/v1/admin/read-aloud/settings", "")
	if d := data(p); d["cache_files"].(float64) != 4 || d["speech_model"] != "tts-fake" {
		t.Fatalf("缓存概况不对: %v", d)
	}
	_, p = e.json(t, e.token, http.MethodDelete, "/api/v1/admin/read-aloud/cache", "")
	if d := data(p); d["cache_files"].(float64) != 0 {
		t.Fatalf("清空缓存失败: %v", d)
	}
	before = tts.count()
	if code, _, _ := e.speak(t, readerToken, doc.ID, "第一段加粗的文字。", "nova"); code != http.StatusOK || tts.count() != before+1 {
		t.Fatalf("清空缓存后应重新合成: %d", code)
	}
	if status, _ := e.json(t, readerToken, http.MethodGet, "/api/v1/admin/read-aloud/settings", ""); status != http.StatusForbidden && status != http.StatusUnauthorized {
		t.Fatalf("普通用户不能访问管理接口: %d", status)
	}

	// 插件关闭后接口不可用
	if status, p := e.json(t, e.token, http.MethodPost, "/api/v1/admin/plugins/read-aloud/uninstall", ""); status != http.StatusOK {
		t.Fatalf("关闭插件失败: %d %v", status, p)
	}
	if status, _ := e.json(t, readerToken, http.MethodGet, "/api/v1/read-aloud/status", ""); status == http.StatusOK {
		t.Fatal("插件关闭后不应可用")
	}
}
