package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"knowforge/server/internal/config"
	"knowforge/server/internal/testdb"
)

// 协作模块集成测试：权限边界 / editor 写权限 / viewer 只读 / 自行退出 / 邀请通知
func TestCollaboration(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("创建应用失败: %v", err)
	}
	ts := httptest.NewServer(a.Router())
	defer ts.Close()

	client := &http.Client{Timeout: 10 * 1e9}
	request := func(method, path string, body any, token string) (int, map[string]any) {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return resp.StatusCode, payload
	}

	// 安装 + alice/bob 注册
	_, install := request(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "协作测试站"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	adminToken := install["data"].(map[string]any)["token"].(string)
	register := func(username string) (string, float64) {
		_, reg := request(http.MethodPost, "/api/v1/auth/register", map[string]any{
			"username": username, "email": username + "@test.local", "password": "secret123",
		}, "")
		data := reg["data"].(map[string]any)
		return data["token"].(string), data["user"].(map[string]any)["id"].(float64)
	}
	aliceToken, _ := register("alice")
	bobToken, bobID := register("bob")

	// alice 建私有书籍与章节
	status, book := request(http.MethodPost, "/api/v1/books", map[string]any{
		"title": "协作之书", "status": "published", "is_public": false,
	}, aliceToken)
	if status != 200 {
		t.Fatalf("建书失败: %d %v", status, book)
	}
	bookData := book["data"].(map[string]any)
	bookID := int(bookData["id"].(float64))
	_, doc := request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), map[string]any{
		"title": "私密章节", "content": "top secret", "status": "published",
	}, aliceToken)
	docID := int(doc["data"].(map[string]any)["id"].(float64))

	// 1. 非协作者：私有书籍 403（书籍存在性不隐藏），创建章节 403
	status, _ = request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d", bookID), nil, bobToken)
	if status != 403 {
		t.Fatalf("非协作者访问私有书籍应 403: %d", status)
	}
	status, _ = request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), map[string]any{
		"title": "hack", "content": "x",
	}, bobToken)
	if status != 403 {
		t.Fatalf("非协作者创建章节应 403: %d", status)
	}

	// 2. 协作者列表：空数组
	status, list := request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d/collaborators", bookID), nil, aliceToken)
	if status != 200 {
		t.Fatalf("协作者列表应可访问: %d", status)
	}
	if _, ok := list["data"].(map[string]any)["collaborators"].([]any); !ok {
		t.Errorf("collaborators 应为 [] 而非 %v", list["data"].(map[string]any)["collaborators"])
	}

	// 3. 非 owner 管理协作者 → 403
	status, _ = request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/collaborators", bookID), map[string]any{
		"username": "bob", "role": "editor",
	}, bobToken)
	if status != 403 {
		t.Fatalf("非所有者添加协作者应 403: %d", status)
	}

	// 4. 邀请 bob 为 editor；待接受期间不得获得私有书籍权限
	status, added := request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/collaborators", bookID), map[string]any{
		"username": "bob", "role": "editor",
	}, aliceToken)
	if status != 200 {
		t.Fatalf("添加协作者失败: %d %v", status, added)
	}
	invitationID := int(added["data"].(map[string]any)["id"].(float64))
	status, _ = request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d", bookID), nil, bobToken)
	if status != 403 {
		t.Fatalf("待接受邀请不应授予私有书籍权限: %d", status)
	}
	status, collaborationBooks := request(http.MethodGet, "/api/v1/books?scope=collaborating", nil, bobToken)
	if status != 200 || len(collaborationBooks["data"].(map[string]any)["items"].([]any)) != 0 {
		t.Fatalf("待接受邀请不应进入协作书籍列表: %d %v", status, collaborationBooks)
	}
	status, searchBeforeAccept := request(http.MethodGet, "/api/v1/search?q=secret", nil, bobToken)
	if status != 200 || searchBeforeAccept["data"].(map[string]any)["total"].(float64) != 0 {
		t.Fatalf("待接受邀请不应通过搜索泄露私有章节: %d %v", status, searchBeforeAccept)
	}
	_, notes := request(http.MethodGet, "/api/v1/notifications", nil, bobToken)
	notif := notes["data"].(map[string]any)["notifications"].([]any)
	if len(notif) != 1 || notif[0].(map[string]any)["type"] != "collaboration" {
		t.Fatalf("应收到 1 条协作邀请通知: %v", notes)
	}
	status, invitations := request(http.MethodGet, "/api/v1/collaboration/invitations", nil, bobToken)
	if status != 200 || len(invitations["data"].(map[string]any)["invitations"].([]any)) != 1 {
		t.Fatalf("bob 应看到 1 条待确认邀请: %d %v", status, invitations)
	}
	status, _ = request(http.MethodPost, fmt.Sprintf("/api/v1/collaboration/invitations/%d/accept", invitationID), nil, aliceToken)
	if status != 404 {
		t.Fatalf("非受邀用户处理邀请应隐藏为 404: %d", status)
	}
	status, _ = request(http.MethodPost, fmt.Sprintf("/api/v1/collaboration/invitations/%d/accept", invitationID), nil, bobToken)
	if status != 200 {
		t.Fatalf("受邀用户接受邀请应成功: %d", status)
	}
	status, collaborationBooks = request(http.MethodGet, "/api/v1/books?scope=collaborating", nil, bobToken)
	items := collaborationBooks["data"].(map[string]any)["items"].([]any)
	if status != 200 || len(items) != 1 || items[0].(map[string]any)["collaborator_role"] != "editor" {
		t.Fatalf("接受后应进入协作书籍列表并携带角色: %d %v", status, collaborationBooks)
	}
	status, searchAfterAccept := request(http.MethodGet, "/api/v1/search?q=secret", nil, bobToken)
	if status != 200 || searchAfterAccept["data"].(map[string]any)["total"].(float64) != 1 {
		t.Fatalf("接受后 editor 应可搜索私有章节: %d %v", status, searchAfterAccept)
	}
	status, _ = request(http.MethodPost, fmt.Sprintf("/api/v1/collaboration/invitations/%d/accept", invitationID), nil, bobToken)
	if status != 404 {
		t.Fatalf("重复处理邀请应返回 404: %d", status)
	}

	// 5. editor：可建章节、看草稿目录，不可改书籍设置/删除书籍
	status, _ = request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), map[string]any{
		"title": "bob 的章节", "content": "hi", "status": "published",
	}, bobToken)
	if status != 200 {
		t.Fatalf("editor 创建章节应成功: %d", status)
	}
	status, _ = request(http.MethodPut, fmt.Sprintf("/api/v1/books/%d", bookID), map[string]any{
		"title": "被篡改",
	}, bobToken)
	if status != 403 {
		t.Fatalf("editor 修改书籍设置应 403: %d", status)
	}

	// 5.1 角色覆盖更新：editor → viewer
	status, _ = request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/collaborators", bookID), map[string]any{
		"username": "bob", "role": "viewer",
	}, aliceToken)
	if status != 200 {
		t.Fatalf("更新协作者角色失败: %d", status)
	}
	status, _ = request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), map[string]any{
		"title": "viewer 越权", "content": "x",
	}, bobToken)
	if status != 403 {
		t.Fatalf("viewer 创建章节应 403: %d", status)
	}

	// 6. viewer：可访问私有书籍与已发布章节
	status, _ = request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d", bookID), nil, bobToken)
	if status != 200 {
		t.Fatalf("viewer 访问私有书籍应 200: %d", status)
	}
	status, _ = request(http.MethodGet, fmt.Sprintf("/api/v1/documents/%d", docID), nil, bobToken)
	if status != 200 {
		t.Fatalf("viewer 访问已发布章节应 200: %d", status)
	}
	status, _ = request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d/collaborators", bookID), nil, bobToken)
	if status != 200 {
		t.Fatalf("协作者查看列表应 200: %d", status)
	}

	// 7. bob 自行退出 → 失去访问权
	status, _ = request(http.MethodDelete, fmt.Sprintf("/api/v1/books/%d/collaborators/%d", bookID, int(bobID)), nil, bobToken)
	if status != 200 {
		t.Fatalf("自行退出应 200: %d", status)
	}
	status, _ = request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d", bookID), nil, bobToken)
	if status != 403 {
		t.Fatalf("退出后访问私有书籍应 403: %d", status)
	}
	status, collaborationBooks = request(http.MethodGet, "/api/v1/books?scope=collaborating", nil, bobToken)
	if status != 200 || len(collaborationBooks["data"].(map[string]any)["items"].([]any)) != 0 {
		t.Fatalf("退出后协作书籍列表应为空: %d %v", status, collaborationBooks)
	}

	// 8. 管理员也可管理协作者；重复移除 404
	status, _ = request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/collaborators", bookID), map[string]any{
		"username": "bob", "role": "viewer",
	}, adminToken)
	if status != 200 {
		t.Fatalf("管理员添加协作者失败: %d", status)
	}
	status, _ = request(http.MethodDelete, fmt.Sprintf("/api/v1/books/%d/collaborators/%d", bookID, int(bobID)), nil, adminToken)
	if status != 200 {
		t.Fatalf("管理员移除协作者失败: %d", status)
	}
	status, _ = request(http.MethodDelete, fmt.Sprintf("/api/v1/books/%d/collaborators/%d", bookID, int(bobID)), nil, adminToken)
	if status != 404 {
		t.Fatalf("重复移除应 404: %d", status)
	}

	// 8.1 拒绝邀请后同样不授予权限
	status, rejectedInvite := request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/collaborators", bookID), map[string]any{
		"username": "bob", "role": "viewer",
	}, aliceToken)
	if status != 200 {
		t.Fatalf("再次邀请失败: %d %v", status, rejectedInvite)
	}
	rejectedID := int(rejectedInvite["data"].(map[string]any)["id"].(float64))
	status, _ = request(http.MethodPost, fmt.Sprintf("/api/v1/collaboration/invitations/%d/reject", rejectedID), nil, bobToken)
	if status != 200 {
		t.Fatalf("拒绝邀请应成功: %d", status)
	}
	status, _ = request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d", bookID), nil, bobToken)
	if status != 403 {
		t.Fatalf("拒绝邀请后不应有私有书籍权限: %d", status)
	}

	// 9. 边界：用户不存在 / 所有者添加自己
	status, _ = request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/collaborators", bookID), map[string]any{
		"username": "ghost", "role": "editor",
	}, aliceToken)
	if status != 404 {
		t.Fatalf("添加不存在的用户应 404: %d", status)
	}
	status, _ = request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/collaborators", bookID), map[string]any{
		"username": "alice", "role": "editor",
	}, aliceToken)
	if status != 400 {
		t.Fatalf("所有者添加自己应 400: %d", status)
	}
}
