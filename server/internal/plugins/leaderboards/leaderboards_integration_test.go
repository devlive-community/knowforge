package leaderboards_test

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

// 集成测试（经 HTTP）：自带榜单（创作、热门、点赞、收藏）与其他插件的榜单（粉丝、关注）、周期统计、
// 只列启用用户与公开书籍、本人名次、管理员开关与排序、公开站点配置、插件禁用。

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
	status, installed := e.req(t, "", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"排行测试"},"admin":{"username":"lb-admin","email":"lb-admin@test.local","password":"secret123"}}`)
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
	u := &models.User{Username: name, Email: name + "@test.local", Role: "user", IsActive: true, EmailVerified: true}
	if err := e.db.Create(u).Error; err != nil {
		t.Fatal(err)
	}
	token, _ := auth.GenerateToken(e.app.Config.Secret, u.ID, u.Username, u.Role)
	return u, token
}

// book 建一本书并发布 chapters 个章节，返回书籍 ID。
func (e *testEnv) book(t *testing.T, token, title string, public bool, chapters int) int {
	t.Helper()
	status := "draft"
	if public {
		status = "published"
	}
	b := e.must(t, token, http.MethodPost, "/api/v1/books", fmt.Sprintf(`{"title":%q,"status":%q,"is_public":%v}`, title, status, public))
	id := int(b["id"].(float64))
	for i := 0; i < chapters; i++ {
		e.must(t, token, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", id), fmt.Sprintf(`{"title":"第%d章","content":"正文","status":"published"}`, i+1))
	}
	return id
}

type row struct {
	Name  string
	Value float64
	Rank  float64
}

func (e *testEnv) board(t *testing.T, token, key, period string) ([]row, map[string]any) {
	t.Helper()
	d := e.must(t, token, http.MethodGet, "/api/v1/leaderboards/"+key+"?period="+period, "")
	out := []row{}
	for _, it := range d["items"].([]any) {
		m := it.(map[string]any)
		name := ""
		if u, ok := m["user"].(map[string]any); ok {
			name = u["username"].(string)
		} else {
			name = m["book"].(map[string]any)["title"].(string)
		}
		out = append(out, row{Name: name, Value: m["value"].(float64), Rank: m["rank"].(float64)})
	}
	return out, d
}

func TestLeaderboards(t *testing.T) {
	e := newTestEnv(t)
	_, aliceToken := e.user(t, "alice")
	_, bobToken := e.user(t, "bob")
	carol, carolToken := e.user(t, "carol")

	bookA := e.book(t, aliceToken, "Alice 的书", true, 3)
	bookB := e.book(t, bobToken, "Bob 的书", true, 1)
	e.book(t, carolToken, "Carol 的私有书", false, 5) // 私有书不计入创作榜、不上书籍榜

	// 列表：默认开放的榜单（成长插件默认关闭，没有经验榜）
	list := e.must(t, "", http.MethodGet, "/api/v1/leaderboards", "")
	keys := []string{}
	for _, it := range list["items"].([]any) {
		keys = append(keys, it.(map[string]any)["key"].(string))
	}
	if fmt.Sprint(keys) != "[followers creators hot-books liked-books favorited-books followed-books]" {
		t.Fatalf("默认开放的榜单与顺序异常: %v", keys)
	}

	// 创作榜：公开书籍中已发布的章节数；本人名次
	rows, d := e.board(t, carolToken, "creators", "all")
	if len(rows) != 2 || rows[0] != (row{"alice", 3, 1}) || rows[1] != (row{"bob", 1, 2}) {
		t.Fatalf("创作榜异常: %+v", rows)
	}
	if me := d["me"].(map[string]any); me["value"] != float64(0) || me["rank"] != nil {
		t.Fatalf("只有私有书的 carol 不应有名次: %v", me)
	}
	if _, d = e.board(t, bobToken, "creators", "week"); d["me"].(map[string]any)["rank"] != float64(2) {
		t.Fatalf("bob 在创作榜应为第 2 名: %v", d["me"])
	}

	// 热门书籍：累计按浏览数，近 7 天按每日统计；私有书不上榜
	e.db.Model(&models.Book{}).Where("id = ?", bookA).Update("view_count", 5)
	e.db.Model(&models.Book{}).Where("id = ?", bookB).Update("view_count", 9)
	today := time.Now().Format("2006-01-02")
	old := time.Now().AddDate(0, 0, -20).Format("2006-01-02")
	e.db.Create(&[]models.BookAnalyticsDaily{
		{BookID: uint(bookA), Day: today, Source: "direct", ViewCount: 4},
		{BookID: uint(bookB), Day: old, Source: "direct", ViewCount: 9},
	})
	if rows, _ = e.board(t, "", "hot-books", "all"); len(rows) != 2 || rows[0].Name != "Bob 的书" || rows[0].Value != 9 {
		t.Fatalf("累计热门应 Bob 的书在前: %+v", rows)
	}
	if rows, _ = e.board(t, "", "hot-books", "week"); len(rows) != 1 || rows[0] != (row{"Alice 的书", 4, 1}) {
		t.Fatalf("近 7 天热门只有 Alice 的书: %+v", rows)
	}
	if rows, d = e.board(t, "", "hot-books", "month"); len(rows) != 2 || d["me"] != nil {
		t.Fatalf("近 30 天两本书都有浏览，书籍榜不带 me: %+v %v", rows, d["me"])
	}

	// 点赞与收藏
	e.must(t, carolToken, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/reactions", bookB), `{"type":"like"}`)
	e.must(t, aliceToken, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/reactions", bookB), `{"type":"like"}`)
	e.must(t, carolToken, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/reactions", bookA), `{"type":"favorite"}`)
	if rows, _ = e.board(t, "", "liked-books", "all"); len(rows) != 1 || rows[0] != (row{"Bob 的书", 2, 1}) {
		t.Fatalf("点赞榜异常: %+v", rows)
	}
	if rows, _ = e.board(t, "", "favorited-books", "week"); len(rows) != 1 || rows[0].Name != "Alice 的书" {
		t.Fatalf("收藏榜异常: %+v", rows)
	}

	// 粉丝榜（用户关注插件）与关注榜（书籍关注插件）；停用的用户不上榜
	e.must(t, bobToken, http.MethodPost, "/api/v1/users/alice/follow", "")
	e.must(t, carolToken, http.MethodPost, "/api/v1/users/alice/follow", "")
	e.must(t, aliceToken, http.MethodPost, "/api/v1/users/bob/follow", "")
	e.must(t, carolToken, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/follow", bookB), "")
	if rows, _ = e.board(t, "", "followers", "all"); len(rows) != 2 || rows[0] != (row{"alice", 2, 1}) {
		t.Fatalf("粉丝榜异常: %+v", rows)
	}
	e.db.Model(&models.User{}).Where("id = ?", carol.ID).Update("is_active", false)
	if rows, _ = e.board(t, "", "followers", "all"); rows[0].Value != 1 {
		t.Fatalf("停用用户的关注不计入粉丝数: %+v", rows)
	}
	if rows, _ = e.board(t, "", "followed-books", "all"); len(rows) != 1 || rows[0].Name != "Bob 的书" {
		t.Fatalf("关注榜异常: %+v", rows)
	}

	// 不支持的周期回退为累计；不存在的榜单 404
	if _, d = e.board(t, "", "creators", "year"); d["period"] != "all" {
		t.Fatalf("不支持的周期应回退为累计: %v", d["period"])
	}
	if status, _ := e.req(t, "", http.MethodGet, "/api/v1/leaderboards/nope", ""); status != http.StatusNotFound {
		t.Fatalf("不存在的榜单应 404，实际 %d", status)
	}

	// 管理员：调整顺序并关闭点赞榜；未提交的榜单保持原状态
	if status, _ := e.req(t, aliceToken, http.MethodGet, "/api/v1/admin/leaderboards", ""); status != http.StatusForbidden {
		t.Fatalf("普通用户不能管理排行榜，实际 %d", status)
	}
	admin := e.must(t, e.admin, http.MethodGet, "/api/v1/admin/leaderboards", "")
	var xp map[string]any
	for _, it := range admin["items"].([]any) {
		if m := it.(map[string]any); m["key"] == "xp" {
			xp = m
		}
	}
	if xp == nil || xp["plugin"] != "growth" || xp["plugin_enabled"] != false || xp["enabled"] != true {
		t.Fatalf("经验榜应已登记但因成长插件未启用而不可用: %v", xp)
	}
	e.must(t, e.admin, http.MethodPut, "/api/v1/admin/leaderboards", `{"boards":[{"key":"hot-books","enabled":true},{"key":"liked-books","enabled":false}]}`)
	if status, _ := e.req(t, e.admin, http.MethodPut, "/api/v1/admin/leaderboards", `{"boards":[{"key":"nope","enabled":true}]}`); status != http.StatusBadRequest {
		t.Fatalf("未知榜单应 400，实际 %d", status)
	}
	_, site := e.req(t, "", http.MethodGet, "/api/v1/site", "")
	if got := fmt.Sprint(site["data"].(map[string]any)["leaderboards"]); got != "[hot-books followers creators favorited-books followed-books]" {
		t.Fatalf("调整后的公开榜单异常: %s", got)
	}
	if status, _ := e.req(t, "", http.MethodGet, "/api/v1/leaderboards/liked-books", ""); status != http.StatusNotFound {
		t.Fatalf("关闭的榜单应 404，实际 %d", status)
	}

	// 插件禁用
	e.must(t, e.admin, http.MethodPost, "/api/v1/admin/plugins/leaderboards/uninstall", "")
	if status, _ := e.req(t, "", http.MethodGet, "/api/v1/leaderboards", ""); status != http.StatusNotFound {
		t.Fatalf("插件禁用后接口应 404，实际 %d", status)
	}
	_, site = e.req(t, "", http.MethodGet, "/api/v1/site", "")
	if got := site["data"].(map[string]any)["leaderboards"].([]any); len(got) != 0 {
		t.Fatalf("插件禁用后公开配置不应有榜单: %v", got)
	}
}
