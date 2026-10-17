package userfollow_test

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
	"knowforge/server/internal/testdb"
)

// 集成测试（经 HTTP）：关注 / 取消关注（幂等、不能关注自己）、关注数与双方状态、关注者与关注列表、
// 关注作者的书籍、被关注与作者更新通知（已关注该书的读者不重复、偏好可关闭）、插件禁用后接口停用。

type testEnv struct {
	app    *app.App
	db     *gorm.DB
	admin  string
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
	status, installed := e.req(t, "", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"关注测试"},"admin":{"username":"follow-admin","email":"follow-admin@test.local","password":"secret123"}}`)
	if status != http.StatusOK {
		t.Fatalf("安装失败: %d %v", status, installed)
	}
	e.admin = installed["data"].(map[string]any)["token"].(string)
	e.db = a.DB
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

func (e *testEnv) user(t *testing.T, name string) (*models.User, string) {
	t.Helper()
	u := &models.User{Username: name, Nickname: name + "-nick", Email: name + "@test.local", Role: "user", IsActive: true, EmailVerified: true}
	if err := e.db.Create(u).Error; err != nil {
		t.Fatal(err)
	}
	token, _ := auth.GenerateToken(e.app.Config.Secret, u.ID, u.Username, u.Role)
	return u, token
}

func (e *testEnv) notices(t *testing.T, userID uint) []models.Notification {
	t.Helper()
	var list []models.Notification
	e.db.Where("user_id = ? AND type = ?", userID, "social").Order("id").Find(&list)
	return list
}

func TestUserFollow(t *testing.T) {
	e := newTestEnv(t)
	author, authorToken := e.user(t, "author")
	reader, readerToken := e.user(t, "reader")
	fan, fanToken := e.user(t, "fan")

	if status, _ := e.req(t, readerToken, http.MethodPost, "/api/v1/users/reader/follow", ""); status != http.StatusBadRequest {
		t.Fatalf("不能关注自己，实际 %d", status)
	}
	if status, _ := e.req(t, "", http.MethodPost, "/api/v1/users/author/follow", ""); status != http.StatusUnauthorized {
		t.Fatalf("未登录不能关注，实际 %d", status)
	}
	if status, _ := e.req(t, readerToken, http.MethodPost, "/api/v1/users/nobody/follow", ""); status != http.StatusNotFound {
		t.Fatalf("关注不存在的用户应 404，实际 %d", status)
	}

	st := e.must(t, readerToken, http.MethodPost, "/api/v1/users/author/follow", "")
	if st["followers"] != float64(1) || st["followed_by_me"] != true {
		t.Fatalf("关注后关注者数应为 1 且已关注: %v", st)
	}
	e.must(t, readerToken, http.MethodPost, "/api/v1/users/author/follow", "") // 幂等
	e.must(t, fanToken, http.MethodPost, "/api/v1/users/author/follow", "")
	e.must(t, authorToken, http.MethodPost, "/api/v1/users/reader/follow", "")
	if n := e.notices(t, author.ID); len(n) != 2 {
		t.Fatalf("作者应收到 2 条被关注通知（重复关注不重复通知），实际 %d", len(n))
	}

	st = e.must(t, "", http.MethodGet, "/api/v1/users/author/follow-stats", "")
	if st["followers"] != float64(2) || st["following"] != float64(1) || st["followed_by_me"] != false {
		t.Fatalf("匿名查看作者关注数异常: %v", st)
	}
	st = e.must(t, readerToken, http.MethodGet, "/api/v1/users/author/follow-stats", "")
	if st["followed_by_me"] != true || st["follows_me"] != true {
		t.Fatalf("读者与作者互相关注: %v", st)
	}

	list := e.must(t, readerToken, http.MethodGet, "/api/v1/users/author/followers", "")
	items := list["items"].([]any)
	if list["total"] != float64(2) || len(items) != 2 {
		t.Fatalf("作者应有 2 位关注者: %v", list)
	}
	first := items[0].(map[string]any)
	if first["user"].(map[string]any)["username"] != "fan" || first["followed_by_me"] != false {
		t.Fatalf("最近关注的 fan 在前，读者未关注 fan: %v", first)
	}
	if items[1].(map[string]any)["is_me"] != true {
		t.Fatalf("列表中的本人应标记 is_me: %v", items[1])
	}
	following := e.must(t, "", http.MethodGet, "/api/v1/users/author/following", "")
	if following["total"] != float64(1) || following["items"].([]any)[0].(map[string]any)["user"].(map[string]any)["username"] != "reader" {
		t.Fatalf("作者关注列表应只有 reader: %v", following)
	}

	// 作者发布公开书籍的新章节：关注者收到通知；已关注该书的 fan 只收到书籍关注通知
	book := e.must(t, authorToken, http.MethodPost, "/api/v1/books", `{"title":"作者的书","status":"published","is_public":true}`)
	bookID := int(book["id"].(float64))
	e.must(t, fanToken, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/follow", bookID), "")
	e.must(t, authorToken, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), `{"title":"第一章","content":"正文","status":"published"}`)
	// 读者先收到作者关注自己的通知，再收到作者更新通知
	if n := e.notices(t, reader.ID); len(n) != 2 || n[0].Title != "author-nick 关注了你" || n[1].Title != "你关注的 author-nick 更新了《作者的书》：第一章" {
		t.Fatalf("读者应收到被关注与作者更新通知: %+v", n)
	}
	if n := e.notices(t, fan.ID); len(n) != 0 {
		t.Fatalf("已关注该书的 fan 不应再收到作者更新通知: %+v", n)
	}

	// 关注作者的书籍
	feed := e.must(t, readerToken, http.MethodGet, "/api/v1/users/me/followed-authors/books", "")
	if feed["total"] != float64(1) || feed["items"].([]any)[0].(map[string]any)["title"] != "作者的书" {
		t.Fatalf("关注作者的书籍应包含公开书: %v", feed)
	}
	e.must(t, authorToken, http.MethodPost, "/api/v1/books", `{"title":"私有草稿","status":"draft"}`)
	if feed = e.must(t, readerToken, http.MethodGet, "/api/v1/users/me/followed-authors/books", ""); feed["total"] != float64(1) {
		t.Fatalf("私有草稿不应出现在关注作者的书籍中: %v", feed)
	}

	// 关闭关注动态通知后不再收到
	e.must(t, readerToken, http.MethodPut, "/api/v1/auth/notification-prefs", `{"comment":true,"reaction":true,"collaboration":true,"moderation":true,"system":true,"social":false}`)
	e.must(t, authorToken, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), `{"title":"第二章","content":"正文","status":"published"}`)
	if n := e.notices(t, reader.ID); len(n) != 2 {
		t.Fatalf("关闭关注动态后不应再收到通知，实际 %d 条", len(n))
	}

	// 取消关注
	st = e.must(t, readerToken, http.MethodDelete, "/api/v1/users/author/follow", "")
	if st["followers"] != float64(1) || st["followed_by_me"] != false {
		t.Fatalf("取消关注后关注者数应为 1: %v", st)
	}

	// 停用的用户不计入关注数
	e.db.Model(&models.User{}).Where("id = ?", fan.ID).Update("is_active", false)
	if st = e.must(t, "", http.MethodGet, "/api/v1/users/author/follow-stats", ""); st["followers"] != float64(0) {
		t.Fatalf("停用用户不应计入关注者: %v", st)
	}

	// 插件禁用后接口停用
	e.must(t, e.admin, http.MethodPost, "/api/v1/admin/plugins/user-follow/uninstall", "")
	if status, _ := e.req(t, "", http.MethodGet, "/api/v1/users/author/follow-stats", ""); status != http.StatusNotFound {
		t.Fatalf("插件禁用后接口应 404，实际 %d", status)
	}
}
