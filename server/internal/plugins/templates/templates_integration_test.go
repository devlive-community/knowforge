package templates_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"knowforge/server/internal/app"
	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// 模板：启用时预置站点模板；个人模板只有自己可见（数量受权益限制）；章节模板替换变量后插入；
// 书籍模板按目录生成草稿章节（追加到目录末尾）；把书保存为模板；站点模板只能由管理员维护。
func TestTemplates(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Router())
	t.Cleanup(srv.Close)
	do := func(token, method, path, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader([]byte(body)))
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
	data := func(p map[string]any) map[string]any { d, _ := p["data"].(map[string]any); return d }
	list := func(v any) []map[string]any {
		out := []map[string]any{}
		arr, _ := v.([]any)
		for _, it := range arr {
			out = append(out, it.(map[string]any))
		}
		return out
	}
	_, installed := do("", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"模板站"},"admin":{"username":"tpl-admin","email":"tpl-admin@test.local","password":"secret123"}}`)
	adminToken := data(installed)["token"].(string)
	user := func(name string) (*models.User, string) {
		u := &models.User{Username: name, Email: name + "@test.local", Nickname: name + "昵称", Role: "user", IsActive: true, EmailVerified: true}
		a.DB.Create(u)
		tok, _ := auth.GenerateToken(a.Config.Secret, u.ID, u.Username, u.Role)
		return u, tok
	}
	_, author := user("tpl-author")
	_, other := user("tpl-other")
	newBook := func(token, title string) uint {
		_, p := do(token, http.MethodPost, "/api/v1/books", fmt.Sprintf(`{"title":%q}`, title))
		return uint(data(p)["id"].(float64))
	}

	if status, _ := do(author, http.MethodGet, "/api/v1/templates", ""); status != http.StatusNotFound {
		t.Fatalf("插件未启用时应不可用: %d", status)
	}
	if status, p := do(adminToken, http.MethodPost, "/api/v1/admin/plugins/templates/install", ""); status != http.StatusOK {
		t.Fatalf("启用插件失败: %d %v", status, p)
	}

	// 启用时预置站点模板（书籍与章节各若干）
	_, p := do(author, http.MethodGet, "/api/v1/templates", "")
	official := list(data(p)["official"])
	kinds := map[string]int{}
	var meeting, product map[string]any
	for _, tp := range official {
		kinds[tp["kind"].(string)]++
		switch tp["title"] {
		case "会议纪要":
			meeting = tp
		case "产品文档":
			product = tp
		}
	}
	if kinds["book"] < 2 || kinds["chapter"] < 3 || meeting == nil || product == nil {
		t.Fatalf("应预置站点模板: %v", kinds)
	}
	if _, has := meeting["content"]; has || meeting["preview"] == "" {
		t.Fatalf("列表不含正文、只给预览: %v", meeting)
	}
	if outline := product["outline"].([]any); len(outline) == 0 || outline[0] != "产品介绍" {
		t.Fatalf("书籍模板应给出第一级目录: %v", product["outline"])
	}
	// 再次启用不会重复预置
	do(adminToken, http.MethodPost, "/api/v1/admin/plugins/templates/uninstall", "")
	do(adminToken, http.MethodPost, "/api/v1/admin/plugins/templates/install", "")
	_, p = do(author, http.MethodGet, "/api/v1/templates", "")
	if n := len(list(data(p)["official"])); n != len(official) {
		t.Fatalf("重新启用不应重复预置: %d → %d", len(official), n)
	}

	// 章节模板：替换变量（时区按浏览器）
	book := newBook(author, "我的手册")
	_, p = do(author, http.MethodPost, fmt.Sprintf("/api/v1/templates/%v/render", meeting["id"]), fmt.Sprintf(`{"book_id":%d,"chapter":"周会","tz":"Asia/Shanghai"}`, book))
	content, _ := data(p)["content"].(string)
	today := time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
	if !strings.HasPrefix(content, "# 周会\n") || !strings.Contains(content, today) || !strings.Contains(content, "tpl-author昵称") || strings.Contains(content, "{{") {
		t.Fatalf("变量未替换: %q", content)
	}
	if status, _ := do(other, http.MethodPost, fmt.Sprintf("/api/v1/templates/%v/render", meeting["id"]), fmt.Sprintf(`{"book_id":%d}`, book)); status != http.StatusNotFound {
		t.Fatalf("不能以别人的书渲染: %d", status)
	}

	// 书籍模板：生成章节（追加到已有章节之后，均为草稿，变量替换为书名）
	do(author, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", book), `{"title":"已有章节"}`)
	status, p := do(author, http.MethodPost, fmt.Sprintf("/api/v1/templates/%v/apply", product["id"]), fmt.Sprintf(`{"book_id":%d}`, book))
	if status != http.StatusOK || data(p)["created"].(float64) != 8 || data(p)["first_slug"] == "" {
		t.Fatalf("按模板生成章节失败: %d %v", status, p)
	}
	var docs []models.Document
	a.DB.Where("book_id = ?", book).Order("id ASC").Find(&docs)
	if len(docs) != 9 || docs[1].Title != "产品介绍" || docs[1].SortOrder <= docs[0].SortOrder || docs[1].Status != "draft" || !strings.Contains(docs[1].Content, "# 我的手册") {
		t.Fatalf("生成的章节不对: %+v", docs[1])
	}
	byTitle := map[string]models.Document{}
	for _, d := range docs {
		byTitle[d.Title] = d
	}
	if p := byTitle["基础功能"].ParentID; p == nil || *p != byTitle["使用指南"].ID {
		t.Fatalf("子章节应挂在父章节下: %+v", byTitle["基础功能"])
	}
	var revisions int64
	a.DB.Model(&models.DocumentRevision{}).Where("document_id = ?", docs[1].ID).Count(&revisions)
	if revisions != 1 {
		t.Fatalf("生成的章节应有首个历史版本: %d", revisions)
	}
	if status, _ := do(other, http.MethodPost, fmt.Sprintf("/api/v1/templates/%v/apply", product["id"]), fmt.Sprintf(`{"book_id":%d}`, book)); status != http.StatusNotFound {
		t.Fatalf("不能往别人的书里生成章节: %d", status)
	}

	// 个人模板：只有自己可见、可改可删
	status, p = do(author, http.MethodPost, "/api/v1/templates", `{"kind":"chapter","title":"读书笔记","content":"# {{chapter}}\n\n{{date}}"}`)
	if status != http.StatusOK {
		t.Fatalf("创建个人模板失败: %d %v", status, p)
	}
	mine := data(p)["id"]
	if status, _ := do(other, http.MethodGet, fmt.Sprintf("/api/v1/templates/%v", mine), ""); status != http.StatusNotFound {
		t.Fatalf("别人的个人模板不可见: %d", status)
	}
	if status, _ := do(other, http.MethodPut, fmt.Sprintf("/api/v1/templates/%v", mine), `{"title":"x"}`); status != http.StatusNotFound {
		t.Fatalf("不能修改别人的模板: %d", status)
	}
	if status, _ := do(author, http.MethodPut, fmt.Sprintf("/api/v1/templates/%v", meeting["id"]), `{"title":"x"}`); status != http.StatusNotFound {
		t.Fatalf("普通用户不能修改站点模板: %d", status)
	}
	if status, p := do(author, http.MethodPut, fmt.Sprintf("/api/v1/templates/%v", mine), `{"title":"读书笔记 v2"}`); status != http.StatusOK || data(p)["title"] != "读书笔记 v2" {
		t.Fatalf("修改个人模板失败: %d %v", status, p)
	}
	if status, _ := do(author, http.MethodPost, "/api/v1/templates", `{"kind":"book","title":"空目录","chapters":[]}`); status != http.StatusBadRequest {
		t.Fatalf("书籍模板至少要有一个章节: %d", status)
	}

	// 把书保存为模板（只有目录，不含正文）；不能保存别人的书
	status, p = do(author, http.MethodPost, "/api/v1/templates/from-book", fmt.Sprintf(`{"book_id":%d,"with_content":false}`, book))
	if status != http.StatusOK || data(p)["title"] != "我的手册" || data(p)["chapter_count"].(float64) != 9 {
		t.Fatalf("把书保存为模板失败: %d %v", status, p)
	}
	chapters := list(data(p)["chapters"])
	if len(chapters) != 7 || chapters[0]["content"] != "" || chapters[4]["title"] != "使用指南" || len(list(chapters[4]["children"])) != 2 {
		t.Fatalf("模板目录不对: %v", chapters)
	}
	if status, _ := do(other, http.MethodPost, "/api/v1/templates/from-book", fmt.Sprintf(`{"book_id":%d}`, book)); status != http.StatusNotFound {
		t.Fatalf("不能把别人的书保存为模板: %d", status)
	}
	_, p = do(author, http.MethodGet, "/api/v1/templates?kind=chapter", "")
	if m := list(data(p)["mine"]); len(m) != 1 || data(p)["used"].(float64) != 2 {
		t.Fatalf("我的章节模板: %v", data(p))
	}

	// 个人模板数量权益
	do(adminToken, http.MethodPut, "/api/v1/admin/entitlements/base", `{"values":{"templates.max":2}}`)
	if status, _ := do(author, http.MethodPost, "/api/v1/templates", `{"kind":"chapter","title":"第三个"}`); status != http.StatusForbidden {
		t.Fatalf("超过个人模板数量上限应拒绝: %d", status)
	}
	if status, _ := do(author, http.MethodDelete, fmt.Sprintf("/api/v1/templates/%v", mine), ""); status != http.StatusOK {
		t.Fatalf("删除个人模板失败: %d", status)
	}

	// 站点模板由管理员维护；普通用户不能调用管理接口
	if status, _ := do(author, http.MethodPost, "/api/v1/admin/templates", `{"kind":"chapter","title":"x"}`); status != http.StatusForbidden {
		t.Fatalf("普通用户不能创建站点模板: %d", status)
	}
	status, p = do(adminToken, http.MethodPost, "/api/v1/admin/templates", `{"kind":"chapter","title":"周报","content":"# {{chapter}}"}`)
	if status != http.StatusOK || data(p)["official"] != true || data(p)["user_id"].(float64) != 0 {
		t.Fatalf("创建站点模板失败: %d %v", status, p)
	}
	weekly := data(p)["id"]
	_, p = do(other, http.MethodGet, "/api/v1/templates?kind=chapter", "")
	found := false
	for _, tp := range list(data(p)["official"]) {
		found = found || tp["id"] == weekly
	}
	if !found {
		t.Fatal("站点模板应对所有作者可见")
	}
	if status, _ := do(adminToken, http.MethodPut, fmt.Sprintf("/api/v1/admin/templates/%v", mine), `{"title":"x"}`); status != http.StatusNotFound {
		t.Fatalf("管理接口只能改站点模板: %d", status)
	}
	if status, _ := do(adminToken, http.MethodDelete, fmt.Sprintf("/api/v1/admin/templates/%v", weekly), ""); status != http.StatusOK {
		t.Fatalf("删除站点模板失败: %d", status)
	}
	var audits int64
	a.DB.Model(&models.AuditLog{}).Where("action LIKE ?", "template.%").Count(&audits)
	if audits != 2 {
		t.Fatalf("站点模板的增删应记入审计日志: %d", audits)
	}
}
