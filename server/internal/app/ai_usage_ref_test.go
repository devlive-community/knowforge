package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugins/booktranslations"
	"knowforge/server/internal/testdb"
)

// AI 调用记录的关联：书籍翻译跳到翻译任务进度，书籍问答跳到书籍「问答」，其他关联书籍的调用显示书名；
// 用户看不到不可读书籍的关联；管理员的调用明细同样带关联。
func TestAIUsageRefs(t *testing.T) {
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
	do := func(token, method, path string) (int, map[string]any) {
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(nil))
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
	body, _ := json.Marshal(map[string]any{"database": testdb.InstallMap(t), "site": map[string]any{"name": "关联"}, "admin": map[string]any{"username": "ref-admin", "email": "ref-admin@test.local", "password": "secret123"}})
	resp, err := http.Post(server.URL+"/api/v1/setup/install", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var installed map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&installed)
	resp.Body.Close()
	adminToken := installed["data"].(map[string]any)["token"].(string)
	for _, key := range []string{"book-translations", "qa"} {
		if status, p := do(adminToken, http.MethodPost, "/api/v1/admin/plugins/"+key+"/install"); status != http.StatusOK {
			t.Fatalf("启用 %s 失败: %d %v", key, status, p)
		}
	}

	u := &models.User{Username: "ref-user", Email: "ref-user@test.local", Role: "user", IsActive: true, EmailVerified: true}
	a.DB.Create(u)
	token, _ := auth.GenerateToken(a.Config.Secret, u.ID, u.Username, u.Role)
	source := models.Book{Title: "原书", Slug: "ref-source", UserID: u.ID, Status: "published", IsPublic: true}
	target := models.Book{Title: "Source (EN)", Slug: "ref-target", UserID: u.ID, Status: "draft"}
	hidden := models.Book{Title: "别人的私有书", Slug: "ref-hidden", UserID: 1, Status: "draft"}
	a.DB.Create(&source)
	a.DB.Create(&target)
	a.DB.Create(&hidden)
	job := booktranslations.TranslateJob{UserID: u.ID, SourceBookID: source.ID, TargetBookID: target.ID, TargetLang: "en", TargetLabel: "English", TraceID: "trace-translate", Status: "running"}
	a.DB.Create(&job)
	for _, l := range []models.AIUsageLog{
		{UserID: u.ID, Feature: "translate.book", RefType: "book", RefID: target.ID, TraceID: "trace-translate", Kind: "chat", Status: "ok"},
		{UserID: u.ID, Feature: "qa.ask", RefType: "book", RefID: source.ID, TraceID: "trace-qa", Kind: "chat", Status: "ok"},
		{UserID: u.ID, Feature: "aiwriter.continue", RefType: "book", RefID: hidden.ID, TraceID: "trace-hidden", Kind: "chat", Status: "ok"},
	} {
		a.DB.Create(&l)
	}

	_, p := do(token, http.MethodGet, "/api/v1/users/me/ai-usage/logs")
	refs := map[string]any{}
	for _, it := range p["data"].(map[string]any)["items"].([]any) {
		tr := it.(map[string]any)
		refs[tr["trace_id"].(string)] = tr["ref"]
	}
	want := map[string]string{
		"trace-translate": fmt.Sprintf("translation|原书 → English|/book/settings/ref-source/ai-translate?job=%d", job.ID),
		"trace-qa":        "qa|原书|/book/detail/ref-source?tab=qa",
	}
	for trace, w := range want {
		r, _ := refs[trace].(map[string]any)
		if r == nil || fmt.Sprintf("%v|%v|%v", r["kind"], r["title"], r["link"]) != w {
			t.Fatalf("%s 的关联应为 %s，实际 %v", trace, w, refs[trace])
		}
	}
	if refs["trace-hidden"] != nil {
		t.Fatalf("不可读书籍不应显示关联: %v", refs["trace-hidden"])
	}

	// 管理员明细：可见全部关联
	_, p = do(adminToken, http.MethodGet, "/api/v1/admin/ai/usage/logs?trace_id=trace-hidden")
	items := p["data"].(map[string]any)["items"].([]any)
	if r, _ := items[0].(map[string]any)["ref"].(map[string]any); r == nil || r["title"] != "别人的私有书" {
		t.Fatalf("管理员应看到关联: %v", items)
	}
}
