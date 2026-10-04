package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"knowforge/server/internal/auth"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

type collabEvent struct {
	Name string
	Data map[string]any
}

// openCollabStream 连接写作台事件流，返回事件通道（后台逐条解析）。
func openCollabStream(t *testing.T, h backupTestClient, token string, bookID, docID uint) (<-chan collabEvent, context.CancelFunc) {
	t.Helper()
	_, tp := h.do(token, http.MethodPost, "/api/v1/stream-tickets", "")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/v1/books/%d/collab/stream?doc_id=%d&ticket=%s", h.server.URL, bookID, docID, backupData(tp)["ticket"]), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("连接事件流失败: %d", resp.StatusCode)
	}
	events := make(chan collabEvent, 64)
	go func() {
		defer resp.Body.Close()
		defer close(events)
		r := bufio.NewReader(resp.Body)
		name := ""
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\n")
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				var data map[string]any
				_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data)
				events <- collabEvent{Name: name, Data: data}
			}
		}
	}()
	return events, cancel
}

// waitEvent 等待满足条件的事件（跳过其他事件）。
func waitEvent(t *testing.T, events <-chan collabEvent, what string, match func(collabEvent) bool) collabEvent {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("等待 %s 时事件流已关闭", what)
			}
			if match(ev) {
				return ev
			}
		case <-timeout:
			t.Fatalf("没有收到 %s", what)
		}
	}
}

func presenceOf(ev collabEvent) []any { list, _ := ev.Data["presence"].([]any); return list }

// 协作写作：保存时按 base_hash 检测他人修改（409 返回最新内容与保存人）；写作台事件流推送在线状态、他人保存与目录变化；
// 不能编辑该书的用户不能连接。
func TestWriterCollaboration(t *testing.T) {
	a, h := newBackupTestApp(t, t.TempDir())
	_, installed := h.do("", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"协作"},"admin":{"username":"wc-admin","email":"wc-admin@test.local","password":"secret123"}}`)
	_ = installed
	newUser := func(name string) (*models.User, string) {
		u := &models.User{Username: name, Email: name + "@test.local", Nickname: name + "昵称", Role: "user", IsActive: true, EmailVerified: true}
		a.DB.Create(u)
		tok, _ := auth.GenerateToken(a.Config.Secret, u.ID, u.Username, u.Role)
		return u, tok
	}
	author, authorToken := newUser("wc-author")
	editor, editorToken := newUser("wc-editor")
	_, outsiderToken := newUser("wc-outsider")
	_, created := h.do(authorToken, http.MethodPost, "/api/v1/books", `{"title":"合写的书"}`)
	bookID := uint(backupData(created)["id"].(float64))
	a.DB.Create(&models.BookCollaborator{BookID: bookID, UserID: editor.ID, Role: "editor", Status: "accepted", InvitedBy: author.ID})
	_, doc := h.do(authorToken, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), `{"title":"第一章","content":"甲\n乙\n丙"}`)
	docID := uint(backupData(doc)["id"].(float64))

	_, loaded := h.do(authorToken, http.MethodGet, fmt.Sprintf("/api/v1/documents/%d", docID), "")
	baseHash := backupData(loaded)["content_hash"].(string)
	if baseHash == "" {
		t.Fatal("读取章节应返回 content_hash")
	}

	// 写作台事件流：作者在线，协作者随后上线
	authorEvents, _ := openCollabStream(t, h, authorToken, bookID, docID)
	hello := waitEvent(t, authorEvents, "hello", func(ev collabEvent) bool { return ev.Name == "hello" })
	authorConn := hello.Data["conn_id"].(string)
	editorEvents, closeEditor := openCollabStream(t, h, editorToken, bookID, docID)
	editorConn := waitEvent(t, editorEvents, "hello", func(ev collabEvent) bool { return ev.Name == "hello" }).Data["conn_id"].(string)
	waitEvent(t, authorEvents, "两人在线", func(ev collabEvent) bool { return ev.Name == "presence" && len(presenceOf(ev)) == 2 })

	// 协作者开始修改：作者看到「有未保存的修改」
	if status, _ := h.do(editorToken, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/collab/presence", bookID), fmt.Sprintf(`{"conn_id":%q,"doc_id":%d,"dirty":true}`, editorConn, docID)); status != http.StatusOK {
		t.Fatalf("更新在线状态失败: %d", status)
	}
	waitEvent(t, authorEvents, "协作者编辑中", func(ev collabEvent) bool {
		for _, p := range presenceOf(ev) {
			m := p.(map[string]any)
			if m["conn_id"] == editorConn && m["dirty"] == true && m["user"].(map[string]any)["display_name"] == "wc-editor昵称" {
				return true
			}
		}
		return false
	})
	if status, _ := h.do(authorToken, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/collab/presence", bookID), fmt.Sprintf(`{"conn_id":%q,"doc_id":%d}`, editorConn, docID)); status != http.StatusNotFound {
		t.Fatalf("不能修改别人的在线状态: %d", status)
	}

	// 协作者先保存：作者收到 doc.saved（发起者凭连接 ID 识别自己的操作）
	req, _ := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/api/v1/documents/%d", h.server.URL, docID), bytes.NewReader([]byte(fmt.Sprintf(`{"content":"甲\n乙改\n丙","base_hash":%q,"create_revision":true}`, baseHash))))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+editorToken)
	req.Header.Set(collabConnHeader, editorConn)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("协作者保存失败: %v %v", err, resp)
	}
	var saved map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&saved)
	resp.Body.Close()
	newHash := backupData(saved)["content_hash"].(string)
	if newHash == "" || newHash == baseHash {
		t.Fatal("保存后应返回新的 content_hash")
	}
	ev := waitEvent(t, authorEvents, "doc.saved", func(ev collabEvent) bool { return ev.Name == "doc.saved" })
	if ev.Data["content_hash"] != newHash || ev.Data["origin"] != editorConn || ev.Data["by"].(map[string]any)["username"] != "wc-editor" {
		t.Fatalf("doc.saved 事件不对: %v", ev.Data)
	}

	// 作者仍按旧内容保存：409，返回最新内容与保存人
	status, conflict := h.do(authorToken, http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", docID), fmt.Sprintf(`{"content":"甲\n乙\n丙丁","base_hash":%q}`, baseHash))
	if status != http.StatusConflict || conflict["code"] != "DOC_CONFLICT" {
		t.Fatalf("应检测到冲突: %d %v", status, conflict)
	}
	cd := backupData(conflict)
	if cd["document"].(map[string]any)["content"] != "甲\n乙改\n丙" || cd["document"].(map[string]any)["content_hash"] != newHash || cd["saved_by"].(map[string]any)["username"] != "wc-editor" {
		t.Fatalf("冲突应返回最新内容与保存人: %v", cd)
	}
	var unchanged models.Document
	a.DB.First(&unchanged, docID)
	if unchanged.Content != "甲\n乙改\n丙" {
		t.Fatalf("冲突时不应写入: %q", unchanged.Content)
	}
	// 合并后按新的摘要保存成功；不带 base_hash 的保存（如调整排序）不受影响
	if status, _ := h.do(authorToken, http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", docID), fmt.Sprintf(`{"content":"甲\n乙改\n丙丁","base_hash":%q}`, newHash)); status != http.StatusOK {
		t.Fatalf("按最新摘要保存应成功: %d", status)
	}
	if status, _ := h.do(authorToken, http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", docID), `{"sort_order":3}`); status != http.StatusOK {
		t.Fatalf("不带摘要的保存应成功: %d", status)
	}
	waitEvent(t, editorEvents, "目录变化", func(ev collabEvent) bool { return ev.Name == "tree" })

	// 新建章节通知目录变化；协作者离开后在线人数减少
	h.do(authorToken, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), `{"title":"第二章"}`)
	waitEvent(t, authorEvents, "新建章节", func(ev collabEvent) bool { return ev.Name == "tree" })
	closeEditor()
	waitEvent(t, authorEvents, "协作者离开", func(ev collabEvent) bool {
		list := presenceOf(ev)
		return ev.Name == "presence" && len(list) == 1 && list[0].(map[string]any)["conn_id"] == authorConn
	})

	// 不能编辑该书的用户不能连接
	_, tp := h.do(outsiderToken, http.MethodPost, "/api/v1/stream-tickets", "")
	r, _ := http.Get(fmt.Sprintf("%s/api/v1/books/%d/collab/stream?ticket=%s", h.server.URL, bookID, backupData(tp)["ticket"]))
	if r.StatusCode != http.StatusForbidden {
		t.Fatalf("非协作者不能连接写作台事件流: %d", r.StatusCode)
	}
	r.Body.Close()
}
