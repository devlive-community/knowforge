package tags_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"gorm.io/gorm"

	"knowforge/server/internal/app"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// 集成测试：启动完整应用（标签插件默认启用），经真实路由验证标签管理与「书籍-标签」扩展点。

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
		t.Fatalf("加载配置失败: %v", err)
	}
	a, err := app.New(cfg)
	if err != nil {
		t.Fatalf("创建应用失败: %v", err)
	}
	e := &testEnv{app: a, server: httptest.NewServer(a.Router()), client: &http.Client{Timeout: 10 * time.Second}}
	t.Cleanup(e.server.Close)
	status, installed := e.do(t, http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"标签测试"},"admin":{"username":"admin","email":"a@b.c","password":"secret123"}}`)
	if status != http.StatusOK {
		t.Fatalf("安装失败: %d %v", status, installed)
	}
	e.token = installed["data"].(map[string]any)["token"].(string)
	e.db = a.DB
	return e
}

func (e *testEnv) do(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, e.server.URL+path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if e.token != "" {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}
	res, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer res.Body.Close()
	payload := map[string]any{}
	_ = json.NewDecoder(res.Body).Decode(&payload)
	return res.StatusCode, payload
}

func tagNames(v any) []string {
	out := []string{}
	list, _ := v.([]any)
	for _, it := range list {
		out = append(out, it.(map[string]any)["name"].(string))
	}
	return out
}

func listTotal(payload map[string]any) int {
	return int(payload["data"].(map[string]any)["total"].(float64))
}

// 后台标签管理：创建（带图标）→ 列表可见 → 更新名称与图标；标签插件禁用后接口 404。
func TestAdminTagManagementAndPluginGate(t *testing.T) {
	e := newTestEnv(t)
	st, payload := e.do(t, http.MethodPost, "/api/v1/admin/tags", `{"name":"Go","icon_type":"fa","icon_value":"fa-code"}`)
	if st != http.StatusOK {
		t.Fatalf("创建标签应 200，实际 %d %v", st, payload)
	}
	data := payload["data"].(map[string]any)
	id := int(data["id"].(float64))
	if data["icon_value"] != "fa-code" {
		t.Fatalf("图标未保存：%v", data)
	}
	st, payload = e.do(t, http.MethodGet, "/api/v1/admin/tags", "")
	if st != http.StatusOK || listTotal(payload) != 1 {
		t.Fatalf("标签列表异常：%d %v", st, payload)
	}
	st, payload = e.do(t, http.MethodPut, "/api/v1/admin/tags/"+strconv.Itoa(id), `{"name":"Golang","icon_type":"image","icon_value":"/uploads/x.png"}`)
	if st != http.StatusOK || payload["data"].(map[string]any)["name"] != "Golang" {
		t.Fatalf("更新标签失败：%d %v", st, payload)
	}
	if st, _ := e.do(t, http.MethodPost, "/api/v1/admin/plugins/tags/uninstall", ""); st != http.StatusOK {
		t.Fatalf("禁用标签插件应 200，实际 %d", st)
	}
	if st, _ := e.do(t, http.MethodGet, "/api/v1/admin/tags", ""); st != http.StatusNotFound {
		t.Fatalf("禁用后标签管理应 404，实际 %d", st)
	}
	if st, _ := e.do(t, http.MethodGet, "/api/v1/tags", ""); st != http.StatusNotFound {
		t.Fatalf("禁用后公开标签列表应 404，实际 %d", st)
	}
}

// 书籍-标签：创建/更新保存标签、详情与列表回填、?tag= 筛选（列表/搜索）、复制带标签、统计、彻底删除清理关联、禁用即无标签。
func TestBookTagsThroughCoreExtensionPoints(t *testing.T) {
	e := newTestEnv(t)

	// 标签字段类型错误按参数错误处理
	if st, _ := e.do(t, http.MethodPost, "/api/v1/books", `{"title":"坏标签","tags":"not-a-list"}`); st != http.StatusBadRequest {
		t.Fatalf("tags 非数组应 400，实际 %d", st)
	}
	st, created := e.do(t, http.MethodPost, "/api/v1/books", `{"title":"标签书","status":"published","is_public":true,"tags":["Go","Web"," Go ",""]}`)
	if st != http.StatusOK {
		t.Fatalf("创建书籍失败: %d %v", st, created)
	}
	book := created["data"].(map[string]any)
	id := int(book["id"].(float64))
	if got := tagNames(book["tags"]); len(got) != 2 || got[0] != "Go" || got[1] != "Web" {
		t.Fatalf("创建响应应带去重后的标签 [Go Web]，实际 %v", got)
	}
	e.do(t, http.MethodPost, "/api/v1/books", `{"title":"无标签书","status":"published","is_public":true}`)

	_, publicTags := e.do(t, http.MethodGet, "/api/v1/tags?page=1&page_size=1", "")
	publicTagPage := publicTags["data"].(map[string]any)
	if publicTagPage["total"] != float64(2) || publicTagPage["page"] != float64(1) || len(publicTagPage["items"].([]any)) != 1 {
		t.Fatalf("公开标签分页异常: %v", publicTagPage)
	}
	_, legacyTags := e.do(t, http.MethodGet, "/api/v1/tags?limit=10", "")
	if _, ok := legacyTags["data"].([]any); !ok {
		t.Fatalf("未请求分页的 /tags 应保持数组响应: %v", legacyTags["data"])
	}

	_, detail := e.do(t, http.MethodGet, fmt.Sprintf("/api/v1/books/%d", id), "")
	if got := tagNames(detail["data"].(map[string]any)["tags"]); len(got) != 2 {
		t.Fatalf("详情应回填标签，实际 %v", got)
	}
	var goSlug string
	e.db.Model(&models.Tag{}).Where("name = ?", "Go").Pluck("slug", &goSlug)
	_, list := e.do(t, http.MethodGet, "/api/v1/books?tag="+goSlug, "")
	if listTotal(list) != 1 || tagNames(list["data"].(map[string]any)["items"].([]any)[0].(map[string]any)["tags"])[0] != "Go" {
		t.Fatalf("?tag= 应只返回带该标签的书并回填标签: %v", list["data"])
	}
	if st, res := e.do(t, http.MethodGet, "/api/v1/search?q=书&type=book&tag="+goSlug, ""); st != http.StatusOK {
		t.Fatalf("按标签搜索失败: %d %v", st, res)
	} else if books := res["data"].(map[string]any)["books"].([]any); len(books) != 1 {
		t.Fatalf("按标签搜索应只命中 1 本，实际 %d", len(books))
	}

	// 更新：只在出现 tags 字段时同步；null 不改动
	e.do(t, http.MethodPut, fmt.Sprintf("/api/v1/books/%d", id), `{"description":"x","tags":null}`)
	_, detail = e.do(t, http.MethodGet, fmt.Sprintf("/api/v1/books/%d", id), "")
	if got := tagNames(detail["data"].(map[string]any)["tags"]); len(got) != 2 {
		t.Fatalf("tags:null 不应改动标签，实际 %v", got)
	}
	st, updated := e.do(t, http.MethodPut, fmt.Sprintf("/api/v1/books/%d", id), `{"tags":["Rust"]}`)
	if st != http.StatusOK || len(tagNames(updated["data"].(map[string]any)["tags"])) != 1 {
		t.Fatalf("更新标签失败: %d %v", st, updated)
	}

	// 复制带标签
	st, copied := e.do(t, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/copy", id), `{"mode":"full"}`)
	if st != http.StatusOK {
		t.Fatalf("复制书籍失败: %d %v", st, copied)
	}
	copyID := int(copied["data"].(map[string]any)["book"].(map[string]any)["id"].(float64))
	_, copyDetail := e.do(t, http.MethodGet, fmt.Sprintf("/api/v1/books/%d", copyID), "")
	if got := tagNames(copyDetail["data"].(map[string]any)["tags"]); len(got) != 1 || got[0] != "Rust" {
		t.Fatalf("复制的书应带源书标签 [Rust]，实际 %v", got)
	}

	// 统计：管理端计全部标签；公开统计只计公开书籍上的标签（复制出的书为私有草稿）
	_, adminStats := e.do(t, http.MethodGet, "/api/v1/admin/stats", "")
	_, siteStats := e.do(t, http.MethodGet, "/api/v1/stats", "")
	if adminStats["data"].(map[string]any)["tag_count"].(float64) != 3 || siteStats["data"].(map[string]any)["tag_count"].(float64) != 1 {
		t.Fatalf("tag_count 统计错误: admin=%v site=%v", adminStats["data"], siteStats["data"])
	}

	// 彻底删除书籍时清理标签关联
	if st, res := e.do(t, http.MethodDelete, fmt.Sprintf("/api/v1/books/%d", copyID), ""); st != http.StatusOK {
		t.Fatalf("删除书籍失败: %d %v", st, res)
	}
	if st, res := e.do(t, http.MethodDelete, fmt.Sprintf("/api/v1/trash/books/%d", copyID), ""); st != http.StatusOK {
		t.Fatalf("彻底删除书籍失败: %d %v", st, res)
	}
	var left int64
	e.db.Model(&models.BookTag{}).Where("book_id = ?", copyID).Count(&left)
	if left != 0 {
		t.Fatalf("彻底删除后标签关联应被清理，剩余 %d", left)
	}

	// 禁用插件：不回填标签、?tag= 不生效、保存 tags 不改动既有关联
	e.do(t, http.MethodPost, "/api/v1/admin/plugins/tags/uninstall", "")
	_, detail = e.do(t, http.MethodGet, fmt.Sprintf("/api/v1/books/%d", id), "")
	if _, has := detail["data"].(map[string]any)["tags"]; has {
		t.Fatalf("禁用后不应回填标签: %v", detail["data"])
	}
	_, list = e.do(t, http.MethodGet, "/api/v1/books?tag="+goSlug, "")
	if listTotal(list) != 2 {
		t.Fatalf("禁用后 ?tag= 不应筛选，实际 total=%d", listTotal(list))
	}
	e.do(t, http.MethodPut, fmt.Sprintf("/api/v1/books/%d", id), `{"tags":[]}`)
	var kept int64
	e.db.Model(&models.BookTag{}).Where("book_id = ?", id).Count(&kept)
	if kept != 1 {
		t.Fatalf("禁用时保存 tags 不应改动既有关联，实际 %d", kept)
	}
}
