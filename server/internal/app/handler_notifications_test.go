package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"knowforge/server/internal/config"
	"knowforge/server/internal/testdb"
)

// 通知模块集成测试：列表空数组 / 评论与点赞触发 / 已读标记 / SSE 首帧与鉴权 / 权限
func TestNotifications(t *testing.T) {
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

	// 安装 + 注册普通用户 alice
	_, install := request(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "通知测试站"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	adminToken := install["data"].(map[string]any)["token"].(string)
	_, reg := request(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "alice", "email": "alice@test.local", "password": "secret123",
	}, "")
	aliceToken := reg["data"].(map[string]any)["token"].(string)

	// 1. 空列表：notifications 必须是数组而非 null
	status, list := request(http.MethodGet, "/api/v1/notifications", nil, aliceToken)
	if status != 200 {
		t.Fatalf("通知列表应可访问: %d", status)
	}
	data := list["data"].(map[string]any)
	if _, ok := data["notifications"].([]any); !ok {
		t.Errorf("notifications 应为 [] 而非 %v", data["notifications"])
	}
	if data["unread_count"].(float64) != 0 {
		t.Errorf("初始未读数应为 0: %v", data["unread_count"])
	}

	// 2. 未登录列表应 401
	status, _ = request(http.MethodGet, "/api/v1/notifications", nil, "")
	if status != 401 {
		t.Fatalf("未登录列表应 401: %d", status)
	}

	// 3. alice 建公开书籍与章节；admin 发评论 → alice 收到通知
	status, book := request(http.MethodPost, "/api/v1/books", map[string]any{
		"title": "通知之书", "status": "published", "is_public": true,
	}, aliceToken)
	if status != 200 {
		t.Fatalf("建书失败: %d %v", status, book)
	}
	bookData := book["data"].(map[string]any)
	bookID := int(bookData["id"].(float64))
	bookSlug := bookData["slug"].(string)
	status, doc := request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), map[string]any{
		"title": "通知章节", "content": "hello", "status": "published",
	}, aliceToken)
	docID := int(doc["data"].(map[string]any)["id"].(float64))
	docSlug := doc["data"].(map[string]any)["slug"].(string)

	request(http.MethodPost, fmt.Sprintf("/api/v1/documents/%d/comments", docID), map[string]any{
		"content": "写得很好",
	}, adminToken)

	status, list = request(http.MethodGet, "/api/v1/notifications", nil, aliceToken)
	data = list["data"].(map[string]any)
	if data["unread_count"].(float64) != 1 {
		t.Fatalf("评论后未读数应为 1: %v", data)
	}
	items := data["notifications"].([]any)
	first := items[0].(map[string]any)
	if first["type"] != "comment" || !strings.Contains(first["title"].(string), "评论") {
		t.Errorf("通知类型/标题异常: %v", first)
	}
	payload := first["payload"].(map[string]any)
	if payload["link"] != fmt.Sprintf("/book/reader/%s/%s", bookSlug, docSlug) {
		t.Errorf("通知 payload link 异常: %v", payload)
	}

	// 3.1 自己评论自己的书不应产生通知
	request(http.MethodPost, fmt.Sprintf("/api/v1/documents/%d/comments", docID), map[string]any{
		"content": "自评",
	}, aliceToken)
	_, list = request(http.MethodGet, "/api/v1/notifications", nil, aliceToken)
	if list["data"].(map[string]any)["unread_count"].(float64) != 1 {
		t.Errorf("自评不应产生通知: %v", list["data"].(map[string]any)["unread_count"])
	}

	// 4. admin 点赞 alice 的书 → 通知；重复点赞不重复通知
	request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/reactions", bookID), map[string]any{"type": "like"}, adminToken)
	request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/reactions", bookID), map[string]any{"type": "like"}, adminToken)
	_, list = request(http.MethodGet, "/api/v1/notifications", nil, aliceToken)
	data = list["data"].(map[string]any)
	if data["unread_count"].(float64) != 2 {
		t.Fatalf("点赞后未读数应为 2（重复点赞不通知）: %v", data)
	}

	// 5. 标记全部已读
	status, read := request(http.MethodPost, "/api/v1/notifications/read", map[string]any{"all": true}, aliceToken)
	if status != 200 || read["data"].(map[string]any)["unread_count"].(float64) != 0 {
		t.Fatalf("全部已读失败: %d %v", status, read)
	}

	// 5.1 指定 ids 标记：两条未读只读其中一条，剩 1 条
	request(http.MethodPost, fmt.Sprintf("/api/v1/documents/%d/comments", docID), map[string]any{
		"content": "再来一条",
	}, adminToken)
	request(http.MethodPost, fmt.Sprintf("/api/v1/documents/%d/comments", docID), map[string]any{
		"content": "又来一条",
	}, adminToken)
	_, list = request(http.MethodGet, "/api/v1/notifications", nil, aliceToken)
	items = list["data"].(map[string]any)["notifications"].([]any)
	if len(items) != 4 {
		t.Fatalf("alice 应有 4 条通知: %d", len(items))
	}
	targetID := uint(items[0].(map[string]any)["id"].(float64))
	status, read = request(http.MethodPost, "/api/v1/notifications/read", map[string]any{"ids": []uint{targetID}}, aliceToken)
	if status != 200 || read["data"].(map[string]any)["unread_count"].(float64) != 1 {
		t.Fatalf("按 ids 已读失败: %d %v", status, read)
	}

	// 6. SSE：无 token 401；带 token 首帧返回 unread_count
	status, _ = request(http.MethodGet, "/api/v1/notifications/stream", nil, "")
	if status != 401 {
		t.Fatalf("SSE 未登录应 401: %d", status)
	}
	// URL 中的登录令牌不再被接受；须先换取短时事件流凭证
	if resp, err := client.Get(ts.URL + "/api/v1/notifications/stream?token=" + aliceToken); err != nil || resp.StatusCode != 401 {
		t.Fatalf("URL 中的登录令牌不应被接受: %v %v", err, resp)
	}
	_, issued := request(http.MethodPost, "/api/v1/stream-tickets", nil, aliceToken)
	ticket := issued["data"].(map[string]any)["ticket"].(string)
	if status, _ := request(http.MethodGet, "/api/v1/auth/me", nil, ticket); status != 401 {
		t.Fatalf("事件流凭证不能当作 API 令牌: %d", status)
	}
	sseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sseReq, _ := http.NewRequestWithContext(sseCtx, http.MethodGet,
		ts.URL+"/api/v1/notifications/stream?ticket="+ticket, nil)
	sseResp, err := client.Do(sseReq)
	if err != nil {
		t.Fatalf("SSE 连接失败: %v", err)
	}
	defer sseResp.Body.Close()
	if !strings.HasPrefix(sseResp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("SSE Content-Type 异常: %s", sseResp.Header.Get("Content-Type"))
	}
	scanner := bufio.NewScanner(sseResp.Body)
	firstFrame := ""
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			firstFrame = strings.TrimPrefix(line, "data: ")
			break
		}
	}
	var frame map[string]any
	if err := json.Unmarshal([]byte(firstFrame), &frame); err != nil {
		t.Fatalf("SSE 首帧解析失败: %q %v", firstFrame, err)
	}
	if frame["unread_count"].(float64) != 1 {
		t.Errorf("SSE 首帧未读数应为 1: %v", frame)
	}
}
