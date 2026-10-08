package teams_test

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

// 集成测试（经 HTTP）：创建团队、邀请与接受、团队书籍按角色授予协作权限（含调整与收回）、直接协作者优先、
// 成员数与团队数权益、退出与转交、插件开关收回/恢复权限、所有者注销后的接任。

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
	status, installed := e.req(t, "", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"团队测试"},"admin":{"username":"tm-admin","email":"tm-admin@test.local","password":"secret123"}}`)
	if status != http.StatusOK {
		t.Fatalf("安装失败: %d %v", status, installed)
	}
	e.token = installed["data"].(map[string]any)["token"].(string)
	e.db = a.DB
	e.must(t, e.token, http.MethodPost, "/api/v1/admin/plugins/teams/install", "")
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

type user struct {
	*models.User
	token string
}

func (e *testEnv) user(t *testing.T, name string) user {
	t.Helper()
	u := &models.User{Username: name, Email: name + "@test.local", Role: "user", IsActive: true, EmailVerified: true}
	if err := e.db.Create(u).Error; err != nil {
		t.Fatal(err)
	}
	token, _ := auth.GenerateToken(e.app.Config.Secret, u.ID, u.Username, u.Role)
	return user{u, token}
}

// role 用户在书上的协作者记录（角色与来源团队）；没有记录返回空串。
func (e *testEnv) role(bookID, userID uint) (string, uint) {
	var c models.BookCollaborator
	if e.db.Where("book_id = ? AND user_id = ? AND status = ?", bookID, userID, "accepted").First(&c).Error != nil {
		return "", 0
	}
	return c.Role, c.TeamID
}

func (e *testEnv) join(t *testing.T, teamID uint, inviter user, invitee user, role string) {
	t.Helper()
	m := e.must(t, inviter.token, http.MethodPost, fmt.Sprintf("/api/v1/teams/%d/members", teamID), fmt.Sprintf(`{"username":%q,"role":%q}`, invitee.Username, role))
	e.must(t, invitee.token, http.MethodPost, fmt.Sprintf("/api/v1/team-invitations/%d/accept", uint(m["id"].(float64))), "")
}

func TestTeams(t *testing.T) {
	e := newTestEnv(t)
	owner, member, admin, direct, outsider := e.user(t, "tm-owner"), e.user(t, "tm-member"), e.user(t, "tm-adminuser"), e.user(t, "tm-direct"), e.user(t, "tm-out")

	team := e.must(t, owner.token, http.MethodPost, "/api/v1/teams", `{"name":"Writers Guild","description":"一起写书"}`)
	teamID := uint(team["id"].(float64))
	if team["slug"] != "writers-guild" || team["my_role"] != "owner" {
		t.Fatalf("创建团队不对: %v", team)
	}
	book := models.Book{Title: "团队书", Slug: "tm-book", UserID: owner.ID, Status: "draft"}
	e.db.Create(&book)
	bookPath := fmt.Sprintf("/api/v1/books/%d", book.ID)

	// 邀请：管理员只能由所有者邀请；接受后才有权限；非成员看不到团队
	e.join(t, teamID, owner, admin, "admin")
	pending := e.must(t, admin.token, http.MethodPost, fmt.Sprintf("/api/v1/teams/%d/members", teamID), `{"username":"tm-member"}`)
	if status, _ := e.req(t, member.token, http.MethodGet, fmt.Sprintf("/api/v1/teams/%d", teamID), ""); status != http.StatusNotFound {
		t.Fatalf("未接受邀请前不能查看团队: %d", status)
	}
	if status, _ := e.req(t, admin.token, http.MethodPost, fmt.Sprintf("/api/v1/teams/%d/members", teamID), `{"username":"tm-out","role":"admin"}`); status != http.StatusForbidden {
		t.Fatalf("管理员不能邀请管理员: %d", status)
	}
	mine := e.must(t, member.token, http.MethodGet, "/api/v1/teams", "")
	if inv := mine["invitations"].([]any); len(inv) != 1 {
		t.Fatalf("应有一条待接受的邀请: %v", mine)
	}
	minePage := e.must(t, member.token, http.MethodGet, "/api/v1/teams?invitations_page=1&invitations_page_size=1", "")
	if minePage["invitations_total"] != float64(1) || minePage["invitations_page"] != float64(1) || len(minePage["invitations"].([]any)) != 1 {
		t.Fatalf("邀请分页响应异常: %v", minePage)
	}
	e.must(t, member.token, http.MethodPost, fmt.Sprintf("/api/v1/team-invitations/%d/accept", uint(pending["id"].(float64))), "")
	teamPage := e.must(t, member.token, http.MethodGet, "/api/v1/teams?teams_page=1&teams_page_size=1", "")
	if teamPage["teams_total"] != float64(1) || teamPage["teams_page"] != float64(1) || len(teamPage["teams"].([]any)) != 1 {
		t.Fatalf("团队分页响应异常: %v", teamPage)
	}

	// 书籍加入团队：管理员为编辑，普通成员按书的成员权限；非成员无权限
	if status, _ := e.req(t, member.token, http.MethodPost, fmt.Sprintf("/api/v1/teams/%d/books", teamID), fmt.Sprintf(`{"book_id":%d}`, book.ID)); status != http.StatusForbidden {
		t.Fatalf("不能把别人的书加入团队: %d", status)
	}
	e.must(t, owner.token, http.MethodPost, fmt.Sprintf("/api/v1/teams/%d/books", teamID), fmt.Sprintf(`{"book_id":%d,"member_role":"suggester"}`, book.ID))
	if r, tid := e.role(book.ID, admin.ID); r != "editor" || tid != teamID {
		t.Fatalf("管理员应获得编辑权限: %s %d", r, tid)
	}
	if r, _ := e.role(book.ID, member.ID); r != "suggester" {
		t.Fatalf("成员应获得建议权限: %s", r)
	}
	if status, _ := e.req(t, member.token, http.MethodGet, bookPath, ""); status != http.StatusOK {
		t.Fatalf("成员应能访问团队的私有书籍: %d", status)
	}
	if status, _ := e.req(t, outsider.token, http.MethodGet, bookPath, ""); status == http.StatusOK {
		t.Fatal("非成员不能访问私有书籍")
	}
	if status, p := e.req(t, member.token, http.MethodGet, "/api/v1/books?scope=collaborating", ""); status != http.StatusOK || p["data"].(map[string]any)["total"].(float64) != 1 {
		t.Fatalf("团队书籍应出现在成员的协作书籍中: %v", p)
	}
	detail := e.must(t, member.token, http.MethodGet, "/api/v1/teams/writers-guild", "")
	if len(detail["books"].([]any)) != 1 || len(detail["members"].([]any)) != 3 || detail["can_manage"] != false {
		t.Fatalf("团队详情不对: %v", detail)
	}

	// 调整成员权限；团队权限不能在书籍协作者中直接修改或移除
	e.must(t, owner.token, http.MethodPut, fmt.Sprintf("/api/v1/teams/%d/books/%d", teamID, book.ID), `{"member_role":"viewer"}`)
	if r, _ := e.role(book.ID, member.ID); r != "viewer" {
		t.Fatalf("成员权限应改为只读: %s", r)
	}
	if status, _ := e.req(t, owner.token, http.MethodDelete, fmt.Sprintf("%s/collaborators/%d", bookPath, member.ID), ""); status != http.StatusConflict {
		t.Fatalf("团队权限不能在协作者中移除: %d", status)
	}
	if status, _ := e.req(t, owner.token, http.MethodPost, bookPath+"/collaborators", `{"username":"tm-member","role":"editor"}`); status != http.StatusConflict {
		t.Fatalf("团队权限不能在协作者中修改: %d", status)
	}

	// 直接协作者优先；移除直接协作后由团队补回
	e.db.Create(&models.BookCollaborator{BookID: book.ID, UserID: direct.ID, Role: "editor", Status: "accepted", InvitedBy: owner.ID})
	e.join(t, teamID, owner, direct, "member")
	if r, tid := e.role(book.ID, direct.ID); r != "editor" || tid != 0 {
		t.Fatalf("直接协作者应保持原样: %s %d", r, tid)
	}
	e.must(t, owner.token, http.MethodDelete, fmt.Sprintf("%s/collaborators/%d", bookPath, direct.ID), "")
	if r, tid := e.role(book.ID, direct.ID); r != "viewer" || tid != teamID {
		t.Fatalf("移除直接协作后应由团队补回: %s %d", r, tid)
	}

	// 权益：成员数（按所有者）与可创建的团队数
	_ = e.app.SetSetting("entitlement_teams_members", "4", "test")
	if status, p := e.req(t, owner.token, http.MethodPost, fmt.Sprintf("/api/v1/teams/%d/members", teamID), `{"username":"tm-out"}`); status != http.StatusForbidden {
		t.Fatalf("超出成员数应拒绝: %d %v", status, p)
	}
	_ = e.app.SetSetting("entitlement_teams_owned", "1", "test")
	if status, _ := e.req(t, owner.token, http.MethodPost, "/api/v1/teams", `{"name":"第二个"}`); status != http.StatusForbidden {
		t.Fatalf("超出团队数应拒绝: %d", status)
	}
	_ = e.app.SetSetting("entitlement_teams_owned", "3", "test")

	// 管理员不能移除管理员；成员退出后收回权限
	if status, _ := e.req(t, admin.token, http.MethodDelete, fmt.Sprintf("/api/v1/teams/%d/members/%d", teamID, owner.ID), ""); status != http.StatusBadRequest {
		t.Fatalf("所有者不能被移除: %d", status)
	}
	e.must(t, member.token, http.MethodDelete, fmt.Sprintf("/api/v1/teams/%d/members/%d", teamID, member.ID), "")
	if r, _ := e.role(book.ID, member.ID); r != "" {
		t.Fatalf("退出后应收回权限: %s", r)
	}

	// 转交：新所有者获得编辑权限，原所有者（书籍作者）改为管理员
	e.must(t, owner.token, http.MethodPost, fmt.Sprintf("/api/v1/teams/%d/transfer", teamID), fmt.Sprintf(`{"user_id":%d}`, direct.ID))
	if r, _ := e.role(book.ID, direct.ID); r != "editor" {
		t.Fatalf("新所有者应为编辑: %s", r)
	}
	if status, _ := e.req(t, owner.token, http.MethodDelete, fmt.Sprintf("/api/v1/teams/%d", teamID), ""); status != http.StatusForbidden {
		t.Fatalf("转交后原所有者不能解散团队: %d", status)
	}

	// 插件关闭收回全部团队权限，重新启用后恢复
	e.must(t, e.token, http.MethodPost, "/api/v1/admin/plugins/teams/uninstall", "")
	if r, _ := e.role(book.ID, admin.ID); r != "" {
		t.Fatalf("插件关闭后应收回团队权限: %s", r)
	}
	e.must(t, e.token, http.MethodPost, "/api/v1/admin/plugins/teams/install", "")
	if r, _ := e.role(book.ID, admin.ID); r != "editor" {
		t.Fatalf("重新启用后应恢复团队权限: %s", r)
	}

	// 解散团队：收回权限，书籍仍归作者
	e.must(t, direct.token, http.MethodDelete, fmt.Sprintf("/api/v1/teams/%d", teamID), "")
	var n int64
	e.db.Model(&models.BookCollaborator{}).Where("team_id = ?", teamID).Count(&n)
	if n != 0 {
		t.Fatalf("解散后应收回全部团队权限: %d", n)
	}
	if status, _ := e.req(t, owner.token, http.MethodGet, bookPath, ""); status != http.StatusOK {
		t.Fatalf("书籍仍归作者: %d", status)
	}

	// 所有者注销（成员记录随账号删除）：由管理员接任
	t2 := e.must(t, outsider.token, http.MethodPost, "/api/v1/teams", `{"name":"接任测试"}`)
	t2ID := uint(t2["id"].(float64))
	e.join(t, t2ID, outsider, admin, "admin")
	e.db.Exec("DELETE FROM team_members WHERE team_id = ? AND user_id = ?", t2ID, outsider.ID)
	d := e.must(t, admin.token, http.MethodGet, fmt.Sprintf("/api/v1/teams/%d", t2ID), "")
	if d["team"].(map[string]any)["owner_id"].(float64) != float64(admin.ID) {
		t.Fatalf("所有者注销后应由管理员接任: %v", d["team"])
	}
}
