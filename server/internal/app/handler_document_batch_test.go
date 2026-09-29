package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

type sseEvent struct {
	name string
	data map[string]any
}

// 章节批量操作：一次请求逐个处理并以事件流推送进度；改状态级联子章节并联动书籍状态，
// 批量删除时已随父章节删除的记为跳过；无权限、参数错误时直接返回 JSON 错误。
func TestBatchDocumentOperations(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Router())
	defer server.Close()
	send := func(token, method, path, body string) *http.Response {
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	do := func(token, method, path, body string) (int, map[string]any) {
		resp := send(token, method, path, body)
		defer resp.Body.Close()
		p := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&p)
		return resp.StatusCode, p
	}
	stream := func(token, path, body string) []sseEvent {
		resp := send(token, http.MethodPost, path, body)
		defer resp.Body.Close()
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			t.Fatalf("应返回事件流: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		var events []sseEvent
		var name string
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				d := map[string]any{}
				_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &d)
				events = append(events, sseEvent{name, d})
			}
		}
		return events
	}
	data := func(p map[string]any) map[string]any { d, _ := p["data"].(map[string]any); return d }
	do("", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"批量"},"admin":{"username":"bt-admin","email":"bt-admin@test.local","password":"secret123"}}`)
	u := &models.User{Username: "bt-user", Email: "bt-user@test.local", Role: "user", IsActive: true, EmailVerified: true}
	other := &models.User{Username: "bt-other", Email: "bt-other@test.local", Role: "user", IsActive: true, EmailVerified: true}
	a.DB.Create(u)
	a.DB.Create(other)
	token, _ := auth.GenerateToken(a.Config.Secret, u.ID, u.Username, u.Role)
	otherToken, _ := auth.GenerateToken(a.Config.Secret, other.ID, other.Username, other.Role)
	_, created := do(token, http.MethodPost, "/api/v1/books", `{"title":"批量书","status":"draft"}`)
	bookID := uint(data(created)["id"].(float64))
	doc := func(title string, parent uint) uint {
		body := fmt.Sprintf(`{"title":%q,"status":"draft"}`, title)
		if parent != 0 {
			body = fmt.Sprintf(`{"title":%q,"status":"draft","parent_id":%d}`, title, parent)
		}
		_, p := do(token, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), body)
		return uint(data(p)["id"].(float64))
	}
	parent := doc("父章", 0)
	child := doc("子章", parent)
	solo := doc("独立章", 0)
	base := fmt.Sprintf("/api/v1/books/%d/documents", bookID)

	// 校验在开始推送前以 JSON 返回
	if status, _ := do(otherToken, http.MethodPost, base+"/batch-status", fmt.Sprintf(`{"ids":[%d],"status":"published"}`, solo)); status != http.StatusForbidden {
		t.Fatalf("无权操作应 403: %d", status)
	}
	for _, body := range []string{`{"ids":[],"status":"published"}`, fmt.Sprintf(`{"ids":[%d],"status":"bad"}`, solo)} {
		if status, _ := do(token, http.MethodPost, base+"/batch-status", body); status != http.StatusBadRequest {
			t.Fatalf("应拒绝 %s: %d", body, status)
		}
	}

	// 批量发布：父章级联子章，书籍从草稿提升为连载中；不存在的章节单独失败
	events := stream(token, base+"/batch-status", fmt.Sprintf(`{"ids":[%d,%d,999999],"status":"published"}`, parent, solo))
	names := []string{}
	for _, e := range events {
		names = append(names, e.name)
	}
	if strings.Join(names, ",") != "start,item,item,item,done" {
		t.Fatalf("事件顺序异常: %v", names)
	}
	if events[1].data["done"].(float64) != 1 || events[1].data["result"].(map[string]any)["status"] != "published" || events[3].data["result"].(map[string]any)["ok"] != false {
		t.Fatalf("进度或结果异常: %v", events)
	}
	if last := events[len(events)-1].data; last["done"].(float64) != 2 || last["failed"].(float64) != 1 {
		t.Fatalf("汇总异常: %v", last)
	}
	for _, id := range []uint{parent, child, solo} {
		var d models.Document
		a.DB.First(&d, id)
		if d.Status != "published" {
			t.Fatalf("章节 %d 应已发布（含级联的子章）: %s", id, d.Status)
		}
	}
	var book models.Book
	a.DB.First(&book, bookID)
	if book.Status != "in_progress" {
		t.Fatalf("发布章节后草稿书籍应提升为连载中: %s", book.Status)
	}

	// 批量删除：父章与子章都选中时，子章随父章删除后记为跳过
	events = stream(token, base+"/batch-delete", fmt.Sprintf(`{"ids":[%d,%d]}`, parent, child))
	if r := events[2].data["result"].(map[string]any); r["skipped"] != true || r["ok"] != true {
		t.Fatalf("随父章删除的子章应记为跳过: %v", events)
	}
	var remaining int64
	a.DB.Model(&models.Document{}).Where("book_id = ?", bookID).Count(&remaining)
	if remaining != 1 {
		t.Fatalf("应只剩独立章，实际 %d", remaining)
	}
}
