package moderation_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gorm.io/gorm"

	"knowforge/server/internal/app"
	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugins/moderation"
	"knowforge/server/internal/testdb"
)

type testEnv struct {
	app    *app.App
	db     *gorm.DB
	token  string
	server *httptest.Server
	client *http.Client
}

func newTestEnv(t *testing.T) *testEnv {
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
	status, installed := e.request(t, "", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"审核测试"},"admin":{"username":"mod-admin","email":"mod-admin@test.local","password":"secret123"}}`)
	if status != http.StatusOK {
		t.Fatalf("安装失败: %d %v", status, installed)
	}
	e.token = installed["data"].(map[string]any)["token"].(string)
	e.db = a.DB
	if status, p := e.request(t, e.token, http.MethodPost, "/api/v1/admin/plugins/moderation/install", ""); status != http.StatusOK {
		t.Fatalf("启用审核插件失败: %d %v", status, p)
	}
	return e
}

func (e *testEnv) request(t *testing.T, token, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, e.server.URL+path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	payload := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&payload)
	return resp.StatusCode, payload
}

func (e *testEnv) as(t *testing.T, u *models.User, method, path, body string) (int, map[string]any) {
	t.Helper()
	token, _ := auth.GenerateToken(e.app.Config.Secret, u.ID, u.Username, u.Role)
	return e.request(t, token, method, path, body)
}

func (e *testEnv) admin(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	return e.request(t, e.token, method, path, body)
}

func data(p map[string]any) map[string]any { d, _ := p["data"].(map[string]any); return d }

func (e *testEnv) docStatus(id uint) string {
	var d models.Document
	e.db.First(&d, id)
	return d.Status
}

func (e *testEnv) caseFor(kind string, id uint) moderation.Case {
	var c moderation.Case
	e.db.Where("kind = ? AND target_id = ?", kind, id).Order("id DESC").First(&c)
	return c
}

func (e *testEnv) notices(userID uint, key string) int64 {
	var n int64
	e.db.Model(&models.Notification{}).Where("user_id = ? AND type = ? AND payload LIKE ?", userID, "moderation", "%"+key+"%").Count(&n)
	return n
}

func TestModerationWorkflow(t *testing.T) {
	e := newTestEnv(t)
	author := &models.User{Username: "writer", Email: "writer@test.local", Role: "user", IsActive: true, EmailVerified: true}
	e.db.Create(author)
	var adminUser models.User
	e.db.Where("username = ?", "mod-admin").First(&adminUser)

	// 词典：批量添加（重复跳过）
	status, added := e.admin(t, http.MethodPost, "/api/v1/admin/moderation/words", `{"words":"敏感词\n违禁，bad\n敏感词","category":"测试"}`)
	if status != http.StatusOK || data(added)["added"].(float64) != 3 {
		t.Fatalf("添加敏感词失败: %d %v", status, added)
	}
	_, tested := e.admin(t, http.MethodPost, "/api/v1/admin/moderation/test", `{"text":"开头\n一个 敏-感-词"}`)
	if hits := data(tested)["hits"].([]any); len(hits) != 1 || hits[0].(map[string]any)["line"].(float64) != 2 {
		t.Fatalf("试审结果错误: %v", tested)
	}

	_, created := e.as(t, author, http.MethodPost, "/api/v1/books", `{"title":"审核之书"}`)
	bookID := uint(data(created)["id"].(float64))
	newDoc := func(body string) (uint, map[string]any) {
		t.Helper()
		status, p := e.as(t, author, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), body)
		if status != http.StatusOK {
			t.Fatalf("创建章节失败: %d %v", status, p)
		}
		return uint(data(p)["id"].(float64)), data(p)
	}

	// 1. 干净内容：直接发布，记入「自动通过」
	clean, _ := newDoc(`{"title":"干净","content":"正常内容","status":"published"}`)
	if e.docStatus(clean) != "published" || e.caseFor("document", clean).Status != moderation.StatusAutoPassed {
		t.Fatalf("干净内容应直接发布并记为自动通过: %s %+v", e.docStatus(clean), e.caseFor("document", clean))
	}

	// 2. 命中：创建即发布被拦截 → 草稿 + 待审核（行列号），通知作者与管理员
	flagged, doc := newDoc(`{"title":"有问题","content":"第一行\n这里有 敏 感 词","status":"published"}`)
	if e.docStatus(flagged) != "draft" || doc["publish_held"] == nil || doc["publish_held"] == "" {
		t.Fatalf("命中敏感词应拦截为草稿并返回说明: %s %v", e.docStatus(flagged), doc)
	}
	c := e.caseFor("document", flagged)
	if c.Status != moderation.StatusPending || len(c.Hits) != 1 || c.Hits[0].Line != 2 || c.Hits[0].Field != "content" {
		t.Fatalf("待审核记录错误: %+v", c)
	}
	if e.notices(author.ID, "notify.moderation.held") != 1 || e.notices(adminUser.ID, "notify.moderation.pending") != 1 {
		t.Fatal("拦截时应通知作者与管理员")
	}
	// 作者改掉敏感内容后再次发布 → 自动通过（同一记录转为自动通过）
	status, updated := e.as(t, author, http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", flagged), `{"content":"已修改","status":"published"}`)
	if status != http.StatusOK || e.docStatus(flagged) != "published" || data(updated)["publish_held"] != nil {
		t.Fatalf("修改后应直接发布: %d %v", status, updated)
	}
	if got := e.caseFor("document", flagged); got.ID != c.ID || got.Status != moderation.StatusAutoPassed {
		t.Fatalf("同一对象应复用记录并转为自动通过: %+v", got)
	}
	// 已发布章节改入敏感词 → 撤回为草稿并待审核
	e.as(t, author, http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", flagged), `{"content":"加入 bad 内容"}`)
	if e.docStatus(flagged) != "draft" || e.caseFor("document", flagged).Status != moderation.StatusPending {
		t.Fatal("已发布章节改入敏感词应撤回待审")
	}

	// 3. 管理员复审通过 → 发布并通知
	caseID := e.caseFor("document", flagged).ID
	if status, p := e.admin(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/moderation/cases/%d/approve", caseID), `{}`); status != http.StatusOK {
		t.Fatalf("复审通过失败: %d %v", status, p)
	}
	if e.docStatus(flagged) != "published" || e.notices(author.ID, "notify.moderation.approved") != 1 {
		t.Fatal("复审通过后应发布并通知作者")
	}
	if status, _ := e.admin(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/moderation/cases/%d/approve", caseID), `{}`); status != http.StatusConflict {
		t.Fatal("已处理的记录不能重复处理")
	}

	// 4. 驳回：需填写意见；待审核驳回保持草稿；自动通过的驳回会撤回发布
	held, _ := newDoc(`{"title":"违禁篇","content":"违禁","status":"published"}`)
	heldCase := e.caseFor("document", held).ID
	if status, _ := e.admin(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/moderation/cases/%d/reject", heldCase), `{}`); status != http.StatusBadRequest {
		t.Fatal("驳回应要求填写意见")
	}
	e.admin(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/moderation/cases/%d/reject", heldCase), `{"note":"请删除违禁内容"}`)
	if e.docStatus(held) != "draft" || e.notices(author.ID, "notify.moderation.rejected") != 1 {
		t.Fatal("驳回后应保持草稿并通知作者")
	}
	autoCase := e.caseFor("document", clean).ID
	e.admin(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/moderation/cases/%d/reject", autoCase), `{"note":"复审不通过"}`)
	if e.docStatus(clean) != "draft" {
		t.Fatal("自动通过的内容复审驳回后应撤回发布")
	}
	_, mine := e.as(t, author, http.MethodGet, "/api/v1/users/me/moderation-cases", "")
	if data(mine)["total"].(float64) != 3 { // 通过 1、驳回 2（自动通过且未复审的不列出）
		t.Fatalf("我的审核记录数量错误: %v", data(mine)["total"])
	}

	// 5. 书籍公开：简介命中 → 保持私有；复审通过后公开
	status, bookResp := e.as(t, author, http.MethodPut, fmt.Sprintf("/api/v1/books/%d", bookID), `{"is_public":true,"status":"published","description":"介绍里有BAD"}`)
	if status != http.StatusOK || data(bookResp)["is_public"] != false || data(bookResp)["publish_held"] == nil {
		t.Fatalf("书籍简介命中应保持私有: %d %v", status, bookResp)
	}
	bookCase := e.caseFor("book", bookID)
	e.admin(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/moderation/cases/%d/approve", bookCase.ID), `{}`)
	var book models.Book
	e.db.First(&book, bookID)
	if !book.IsPublic || book.Status != "published" {
		t.Fatalf("复审通过后书籍应公开: %+v", book)
	}

	// 6. 管理员免审；关闭章节审查；禁用插件均放行
	adminBookStatus, adminBook := e.admin(t, http.MethodPost, "/api/v1/books", `{"title":"管理员的书"}`)
	adminBookID := uint(data(adminBook)["id"].(float64))
	_ = adminBookStatus
	_, adminDoc := e.admin(t, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", adminBookID), `{"title":"x","content":"违禁","status":"published"}`)
	if e.docStatus(uint(data(adminDoc)["id"].(float64))) != "published" {
		t.Fatal("管理员发布默认免审")
	}
	e.admin(t, http.MethodPut, "/api/v1/admin/moderation/settings", `{"scope_documents":false}`)
	off, _ := newDoc(`{"title":"关闭审查","content":"违禁","status":"published"}`)
	if e.docStatus(off) != "published" {
		t.Fatal("关闭章节审查后应直接发布")
	}
	e.admin(t, http.MethodPut, "/api/v1/admin/moderation/settings", `{"scope_documents":true}`)
	e.admin(t, http.MethodPost, "/api/v1/admin/plugins/moderation/uninstall", "")
	disabled, _ := newDoc(`{"title":"插件禁用","content":"违禁","status":"published"}`)
	if e.docStatus(disabled) != "published" {
		t.Fatal("插件禁用后不再审查")
	}
}
