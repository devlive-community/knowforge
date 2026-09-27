package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"knowforge/server/internal/config"
	"knowforge/server/internal/testdb"
)

// TestAuthorizationBoundaries 覆盖公开数据、对象归属和令牌回跳地址的安全边界。
func TestAuthorizationBoundaries(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &mailRecorder{}
	a.MailSender = recorder
	ts := httptest.NewServer(a.Router())
	defer ts.Close()

	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	request := func(method, path string, body any, token string) (int, map[string]any, http.Header) {
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
		return resp.StatusCode, payload, resp.Header
	}

	_, install, _ := request(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "权限测试站"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	adminToken := install["data"].(map[string]any)["token"].(string)
	register := func(username string) string {
		_, result, _ := request(http.MethodPost, "/api/v1/auth/register", map[string]any{
			"username": username, "email": username + "@test.local", "password": "secret123",
		}, "")
		return result["data"].(map[string]any)["token"].(string)
	}
	aliceToken := register("alice")
	bobToken := register("bob")
	charlieToken := register("charlie")

	// 安装完成后不公开服务端路径，也不再允许匿名数据库连接探测。
	status, setup, _ := request(http.MethodGet, "/api/v1/setup/status", nil, "")
	setupData := setup["data"].(map[string]any)
	if status != http.StatusOK || setupData["data_dir"] != nil || setupData["sqlite_default_path"] != nil {
		t.Fatalf("安装后 setup/status 不应公开路径: %d %v", status, setupData)
	}
	status, _, _ = request(http.MethodPost, "/api/v1/setup/test-connection", map[string]any{"type": "sqlite"}, "")
	if status != http.StatusNotFound {
		t.Fatalf("安装后数据库测试接口应关闭: %d", status)
	}

	// 外部 origin 不得控制 OAuth 和找回密码的令牌回跳地址。
	status, _, headers := request(http.MethodGet, "/api/v1/auth/oauth/github?origin="+url.QueryEscape("https://evil.example"), nil, "")
	if status != http.StatusFound || !strings.HasPrefix(headers.Get("Location"), ts.URL+"/login") {
		t.Fatalf("OAuth 应回到本站而非外部 origin: %d %s", status, headers.Get("Location"))
	}
	request(http.MethodPost, "/api/v1/auth/password/forgot?origin="+url.QueryEscape("https://evil.example"), map[string]any{"email": "alice@test.local"}, "")
	if ran, err := a.Jobs.RunOnce(context.Background()); err != nil || !ran {
		t.Fatalf("执行找回密码邮件任务失败: ran=%v err=%v", ran, err)
	}
	if len(recorder.sends) != 1 || !strings.Contains(recorder.sends[0], ts.URL+"/reset-password?token=") || strings.Contains(recorder.sends[0], "evil.example") {
		t.Fatalf("找回密码链接必须使用本站地址: %v", recorder.sends)
	}

	createBook := func(title, slug, bookStatus string, public bool) (int, map[string]any) {
		_, payload, _ := request(http.MethodPost, "/api/v1/books", map[string]any{
			"title": title, "slug": slug, "status": bookStatus, "is_public": public,
		}, aliceToken)
		data := payload["data"].(map[string]any)
		return int(data["id"].(float64)), data
	}
	createDoc := func(bookID int, title, content, docStatus string, allowComments bool) map[string]any {
		_, payload, _ := request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), map[string]any{
			"title": title, "content": content, "status": docStatus, "allow_comments": allowComments,
		}, aliceToken)
		return payload["data"].(map[string]any)
	}

	// is_public 不代表已发布：匿名搜索不得命中公开草稿书或草稿章节正文。
	draftBookID, _ := createBook("leaksecret 草稿书", "draft-public", "draft", true)
	createDoc(draftBookID, "草稿章节", "leaksecret 正文", "draft", true)
	status, search, _ := request(http.MethodGet, "/api/v1/search?q=leaksecret", nil, "")
	searchData := search["data"].(map[string]any)
	if status != http.StatusOK || len(searchData["books"].([]any)) != 0 || len(searchData["documents"].([]any)) != 0 {
		t.Fatalf("匿名搜索泄露未发布内容: %v", searchData)
	}

	publicBookID, _ := createBook("公开书", "public-book", "published", true)
	publicDoc := createDoc(publicBookID, "公开章节", "公开正文", "published", true)
	publicDocID := int(publicDoc["id"].(float64))

	// 进行中与已完成是书籍级创作状态，两者在公开开关开启后都应可被发现和阅读。
	for _, bookStatus := range []string{"in_progress", "completed"} {
		title := "状态可见 " + bookStatus
		bookID, _ := createBook(title, "visible-"+strings.ReplaceAll(bookStatus, "_", "-"), bookStatus, true)
		createDoc(bookID, "状态章节", "状态正文 "+bookStatus, "published", true)
		status, visibleBook, _ := request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d", bookID), nil, "")
		if status != http.StatusOK || visibleBook["data"].(map[string]any)["status"] != bookStatus {
			t.Fatalf("公开 %s 书籍应可访问: %d %v", bookStatus, status, visibleBook)
		}
		status, list, _ := request(http.MethodGet, "/api/v1/books?title="+url.QueryEscape(title), nil, "")
		items := list["data"].(map[string]any)["items"].([]any)
		if status != http.StatusOK || len(items) != 1 {
			t.Fatalf("公开列表应包含 %s 书籍: %d %v", bookStatus, status, list)
		}
		status, docs, _ := request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d/documents", bookID), nil, "")
		if status != http.StatusOK || len(docs["data"].([]any)) != 1 {
			t.Fatalf("公开 %s 书籍的已发布章节应可阅读: %d %v", bookStatus, status, docs)
		}
	}
	status, _, _ = request(http.MethodPost, "/api/v1/books", map[string]any{
		"title": "非法状态", "status": "paused",
	}, aliceToken)
	if status != http.StatusBadRequest {
		t.Fatalf("未定义的书籍状态应拒绝: %d", status)
	}

	// 公共书籍嵌套作者资料不得包含真实邮箱。
	status, publicBook, _ := request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d", publicBookID), nil, "")
	author := publicBook["data"].(map[string]any)["user"].(map[string]any)
	if status != http.StatusOK || author["email"] == "alice@test.local" {
		t.Fatalf("公共书籍泄露作者邮箱: %v", author)
	}

	// 访问能力由服务端计算，普通读者不可管理，所有者可管理，editor 只能编辑内容。
	_, bobAccessPayload, _ := request(http.MethodGet, "/api/v1/books/slug/public-book/access", nil, bobToken)
	bobAccess := bobAccessPayload["data"].(map[string]any)
	if bobAccess["can_manage"] != false || bobAccess["can_edit_content"] != false {
		t.Fatalf("普通读者不应拥有管理或编辑能力: %v", bobAccess)
	}
	_, aliceAccessPayload, _ := request(http.MethodGet, "/api/v1/books/slug/public-book/access", nil, aliceToken)
	if aliceAccessPayload["data"].(map[string]any)["can_manage"] != true {
		t.Fatal("书籍所有者应拥有管理能力")
	}
	_, editorInvitePayload, _ := request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/collaborators", publicBookID), map[string]any{"username": "charlie", "role": "editor"}, aliceToken)
	editorInviteID := int(editorInvitePayload["data"].(map[string]any)["id"].(float64))
	request(http.MethodPost, fmt.Sprintf("/api/v1/collaboration/invitations/%d/accept", editorInviteID), nil, charlieToken)
	_, editorAccessPayload, _ := request(http.MethodGet, "/api/v1/books/slug/public-book/access", nil, charlieToken)
	editorAccess := editorAccessPayload["data"].(map[string]any)
	if editorAccess["can_edit_content"] != true || editorAccess["can_manage"] != false {
		t.Fatalf("editor 能力应只允许内容编辑: %v", editorAccess)
	}

	// 评论响应只返回公开用户字段；作者可删自己的评论，第三方不可删。
	status, createdComment, _ := request(http.MethodPost, fmt.Sprintf("/api/v1/documents/%d/comments", publicDocID), map[string]any{"content": "bob comment"}, bobToken)
	if status != http.StatusOK {
		t.Fatalf("公开章节评论失败: %d %v", status, createdComment)
	}
	commentID := int(createdComment["data"].(map[string]any)["id"].(float64))
	status, comments, _ := request(http.MethodGet, fmt.Sprintf("/api/v1/documents/%d/comments", publicDocID), nil, "")
	commentUser := comments["data"].([]any)[0].(map[string]any)["user"].(map[string]any)
	if status != http.StatusOK || commentUser["email"] != nil {
		t.Fatalf("评论响应不应包含邮箱: %v", commentUser)
	}
	status, _, _ = request(http.MethodDelete, fmt.Sprintf("/api/v1/comments/%d", commentID), nil, charlieToken)
	if status != http.StatusForbidden {
		t.Fatalf("第三方删除评论应 403: %d", status)
	}
	status, _, _ = request(http.MethodDelete, fmt.Sprintf("/api/v1/comments/%d", commentID), nil, bobToken)
	if status != http.StatusOK {
		t.Fatalf("评论作者删除自己的评论应成功: %d", status)
	}

	closedDoc := createDoc(publicBookID, "关闭评论", "正文", "published", false)
	status, _, _ = request(http.MethodPost, fmt.Sprintf("/api/v1/documents/%d/comments", int(closedDoc["id"].(float64))), map[string]any{"content": "不应写入"}, bobToken)
	if status != http.StatusForbidden {
		t.Fatalf("关闭评论后应拒绝写入: %d", status)
	}

	// 私有书籍的目录、评论、互动和浏览统计均不可由无权限用户访问或写入。
	privateBookID, _ := createBook("私有书", "private-book", "published", false)
	privateDoc := createDoc(privateBookID, "私有章节", "私有正文", "published", true)
	privateDocID := int(privateDoc["id"].(float64))
	checks := []struct {
		method string
		path   string
		body   any
		token  string
	}{
		{http.MethodGet, fmt.Sprintf("/api/v1/books/%d/documents", privateBookID), nil, ""},
		{http.MethodGet, fmt.Sprintf("/api/v1/documents/%d/comments", privateDocID), nil, bobToken},
		{http.MethodPost, fmt.Sprintf("/api/v1/documents/%d/comments", privateDocID), map[string]any{"content": "越权评论"}, bobToken},
		{http.MethodPost, fmt.Sprintf("/api/v1/books/%d/reactions", privateBookID), map[string]any{"type": "favorite"}, bobToken},
		{http.MethodPost, fmt.Sprintf("/api/v1/books/%d/view", privateBookID), nil, ""},
	}
	for _, check := range checks {
		status, _, _ = request(check.method, check.path, check.body, check.token)
		if status != http.StatusNotFound {
			t.Fatalf("越权请求应隐藏资源存在性，%s %s 返回 %d", check.method, check.path, status)
		}
	}

	// 阅读进度只能引用当前书中且当前用户可读的真实章节，标题与 slug 以服务端数据为准。
	status, _, _ = request(http.MethodPut, fmt.Sprintf("/api/v1/reading-progress/%d", publicBookID), map[string]any{
		"doc_id": privateDocID, "doc_slug": "forged", "doc_title": "forged",
	}, bobToken)
	if status != http.StatusNotFound {
		t.Fatalf("跨书章节不得写入阅读进度: %d", status)
	}

	// 书籍转私有后，旧收藏列表必须过滤已经失去访问权的书籍。
	request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/reactions", publicBookID), map[string]any{"type": "favorite"}, bobToken)
	if _, favorites, _ := request(http.MethodGet, "/api/v1/users/me/reactions?type=favorite", nil, bobToken); favorites["data"].(map[string]any)["total"].(float64) != 1 {
		t.Fatalf("收藏列表的总数应为 1: %v", favorites)
	}
	request(http.MethodPut, fmt.Sprintf("/api/v1/books/%d", publicBookID), map[string]any{"is_public": false}, aliceToken)
	status, favorites, _ := request(http.MethodGet, "/api/v1/users/me/reactions?type=favorite", nil, bobToken)
	if status != http.StatusOK || favorites["data"].(map[string]any)["total"].(float64) != 0 {
		t.Fatalf("收藏列表泄露已转私有书籍: %v", favorites)
	}

	_ = adminToken
}
