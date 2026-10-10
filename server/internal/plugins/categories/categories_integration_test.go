package categories_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"knowforge/server/internal/app"
	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// 集成测试（经 HTTP）：分类树（三层上限、防止移到子分类下、slug 唯一）、书籍选择 / 清除分类、
// 详情与列表回填分类路径、/books?category= 含子分类、删除保护、批量归类与未分类筛选、Sitemap 与插件开关。

type testEnv struct {
	app    *app.App
	db     *gorm.DB
	token  string
	server *httptest.Server
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
	e := &testEnv{app: a, server: httptest.NewServer(a.Router())}
	t.Cleanup(e.server.Close)
	status, installed := e.req(t, "", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"分类测试"},"admin":{"username":"cat-admin","email":"cat-admin@test.local","password":"secret123"}}`)
	if status != http.StatusOK {
		t.Fatalf("安装失败: %d %v", status, installed)
	}
	e.token = installed["data"].(map[string]any)["token"].(string)
	e.db = a.DB
	e.must(t, e.token, http.MethodPost, "/api/v1/admin/plugins/categories/install", "")
	return e
}

func (e *testEnv) req(t *testing.T, token, method, path, body string) (int, map[string]any) {
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
	defer resp.Body.Close()
	p := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&p)
	return resp.StatusCode, p
}

func (e *testEnv) must(t *testing.T, token, method, path, body string) map[string]any {
	t.Helper()
	status, p := e.req(t, token, method, path, body)
	if status != http.StatusOK {
		t.Fatalf("%s %s 失败: %d %v", method, path, status, p)
	}
	d, _ := p["data"].(map[string]any)
	return d
}

func (e *testEnv) category(t *testing.T, body string) uint {
	t.Helper()
	return uint(e.must(t, e.token, http.MethodPost, "/api/v1/admin/categories", body)["id"].(float64))
}

func bookTitles(p map[string]any) []string {
	out := []string{}
	for _, it := range p["data"].(map[string]any)["items"].([]any) {
		out = append(out, it.(map[string]any)["title"].(string))
	}
	return out
}

func TestCategories(t *testing.T) {
	e := newTestEnv(t)
	u := &models.User{Username: "cat-author", Email: "cat-author@test.local", Role: "user", IsActive: true, EmailVerified: true}
	e.db.Create(u)
	token, _ := auth.GenerateToken(e.app.Config.Secret, u.ID, u.Username, u.Role)

	// 分类树：最多三层，slug 由名称生成且唯一
	tech := e.category(t, `{"name":"Tech"}`)
	backend := e.category(t, fmt.Sprintf(`{"name":"Backend","parent_id":%d}`, tech))
	golang := e.category(t, fmt.Sprintf(`{"name":"Go","slug":"go","parent_id":%d}`, backend))
	lit := e.category(t, `{"name":"文学","slug":"literature"}`)
	if status, _ := e.req(t, e.token, http.MethodPost, "/api/v1/admin/categories", fmt.Sprintf(`{"name":"Too deep","parent_id":%d}`, golang)); status != http.StatusBadRequest {
		t.Fatalf("超过三层应拒绝: %d", status)
	}
	if status, _ := e.req(t, e.token, http.MethodPost, "/api/v1/admin/categories", `{"name":"Dup","slug":"go"}`); status != http.StatusConflict {
		t.Fatalf("slug 重复应拒绝: %d", status)
	}
	if status, _ := e.req(t, e.token, http.MethodPut, fmt.Sprintf("/api/v1/admin/categories/%d", tech), fmt.Sprintf(`{"name":"Tech","parent_id":%d}`, backend)); status != http.StatusBadRequest {
		t.Fatalf("不能移到自己的子分类下: %d", status)
	}
	if status, _ := e.req(t, e.token, http.MethodPut, fmt.Sprintf("/api/v1/admin/categories/%d", backend), fmt.Sprintf(`{"name":"Backend","parent_id":%d}`, lit)); status != http.StatusOK {
		t.Fatalf("移动后仍在三层内应允许: %d", status)
	}
	e.must(t, e.token, http.MethodPut, fmt.Sprintf("/api/v1/admin/categories/%d", backend), fmt.Sprintf(`{"name":"Backend","parent_id":%d}`, tech))

	// 书籍选择分类（可选）：创建时选择，详情回填路径
	created := e.must(t, token, http.MethodPost, "/api/v1/books", fmt.Sprintf(`{"title":"Go 实战","is_public":true,"status":"published","category_id":%d}`, golang))
	goBook := uint(created["id"].(float64))
	e.must(t, token, http.MethodPost, "/api/v1/books", `{"title":"散文集","is_public":true,"status":"published"}`)
	detail := e.must(t, token, http.MethodGet, fmt.Sprintf("/api/v1/books/%d", goBook), "")
	cat, _ := detail["category"].(map[string]any)
	if cat == nil || cat["slug"] != "go" || len(cat["path"].([]any)) != 3 || cat["path"].([]any)[0].(map[string]any)["slug"] != "tech" {
		t.Fatalf("详情应回填分类路径: %v", detail["category"])
	}

	// /books?category= 含子分类；未知分类为空
	_, p := e.req(t, "", http.MethodGet, "/api/v1/books?category=tech", "")
	if titles := bookTitles(p); len(titles) != 1 || titles[0] != "Go 实战" {
		t.Fatalf("按上级分类应包含子分类的书: %v", titles)
	}
	_, p = e.req(t, "", http.MethodGet, "/api/v1/books?category=literature", "")
	if titles := bookTitles(p); len(titles) != 0 {
		t.Fatalf("其他分类不应有这本书: %v", titles)
	}
	_, p = e.req(t, "", http.MethodGet, "/api/v1/books?category=nope", "")
	if titles := bookTitles(p); len(titles) != 0 {
		t.Fatalf("未知分类应为空: %v", titles)
	}

	// 公开分类树：书籍数累加到上级
	tree := e.must(t, "", http.MethodGet, "/api/v1/categories", "")["items"].([]any)
	if len(tree) != 2 || tree[0].(map[string]any)["book_count"].(float64) != 1 {
		t.Fatalf("分类树不对: %v", tree)
	}
	one := e.must(t, "", http.MethodGet, "/api/v1/categories/backend", "")
	if len(one["path"].([]any)) != 2 || len(one["children"].([]any)) != 1 {
		t.Fatalf("分类详情不对: %v", one)
	}

	// 删除保护：有子分类或有书都不能删
	if status, _ := e.req(t, e.token, http.MethodDelete, fmt.Sprintf("/api/v1/admin/categories/%d", backend), ""); status != http.StatusConflict {
		t.Fatalf("有子分类时不能删除: %d", status)
	}
	if status, p := e.req(t, e.token, http.MethodDelete, fmt.Sprintf("/api/v1/admin/categories/%d", golang), ""); status != http.StatusConflict || !strings.Contains(p["message"].(string), "1 本书") {
		t.Fatalf("有书时不能删除: %d %v", status, p)
	}

	// 作者把书改为未分类
	e.must(t, token, http.MethodPut, fmt.Sprintf("/api/v1/books/%d", goBook), `{"category_id":0}`)
	if d := e.must(t, token, http.MethodGet, fmt.Sprintf("/api/v1/books/%d", goBook), ""); d["category"] != nil {
		t.Fatalf("改为未分类后不应有分类: %v", d["category"])
	}

	// 批量归类：筛选未分类的书并归入「文学」
	_, p = e.req(t, e.token, http.MethodGet, "/api/v1/admin/category-books?category=none&page_size=50", "")
	ids := []string{}
	for _, it := range p["data"].(map[string]any)["items"].([]any) {
		ids = append(ids, fmt.Sprint(it.(map[string]any)["id"]))
	}
	if len(ids) != 2 {
		t.Fatalf("应有 2 本未分类的书: %v", ids)
	}
	e.must(t, e.token, http.MethodPost, "/api/v1/admin/category-books/assign", fmt.Sprintf(`{"book_ids":[%s],"category_id":%d}`, strings.Join(ids, ","), lit))
	admin := e.must(t, e.token, http.MethodGet, "/api/v1/admin/categories", "")
	if admin["uncategorized"].(float64) != 0 {
		t.Fatalf("批量归类后不应有未分类的书: %v", admin["uncategorized"])
	}
	if status, _ := e.req(t, token, http.MethodPost, "/api/v1/admin/category-books/assign", fmt.Sprintf(`{"book_ids":[%d],"category_id":0}`, goBook)); status != http.StatusForbidden && status != http.StatusUnauthorized {
		t.Fatalf("普通用户不能批量归类: %d", status)
	}

	// Sitemap 包含分类页
	sm := e.must(t, "", http.MethodGet, "/api/v1/sitemap", "")
	found := false
	for _, it := range sm["entries"].([]any) {
		if it.(map[string]any)["path"] == "/explore?category=literature" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Sitemap 应包含分类页: %v", sm["entries"])
	}

	// 插件禁用：不再回填分类，筛选参数被忽略，接口停用
	e.must(t, e.token, http.MethodPost, "/api/v1/admin/plugins/categories/uninstall", "")
	if d := e.must(t, token, http.MethodGet, fmt.Sprintf("/api/v1/books/%d", goBook), ""); d["category"] != nil {
		t.Fatalf("禁用后不应回填分类: %v", d["category"])
	}
	_, p = e.req(t, "", http.MethodGet, "/api/v1/books?category=literature", "")
	if titles := bookTitles(p); len(titles) != 2 {
		t.Fatalf("禁用后应忽略分类筛选: %v", titles)
	}
	if status, _ := e.req(t, "", http.MethodGet, "/api/v1/categories", ""); status == http.StatusOK {
		t.Fatal("禁用后分类接口应停用")
	}
}
