package app

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"knowforge/server/internal/auth"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// 修改建议：「建议者」能进入写作台（看草稿、批注）但不能直接保存，只能提交建议；作者审阅后记录采纳结果并通知建议者；
// 建议者可撤回未处理的建议。批注中 @ 提及能参与写作的人时，对方收到「提到了你」的通知（不重复收到普通通知）。
func TestWriterSuggestions(t *testing.T) {
	a, h := newBackupTestApp(t, t.TempDir())
	h.do("", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"建议"},"admin":{"username":"sg-admin","email":"sg-admin@test.local","password":"secret123"}}`)
	newUser := func(name string) (*models.User, string) {
		u := &models.User{Username: name, Email: name + "@test.local", Role: "user", IsActive: true, EmailVerified: true}
		a.DB.Create(u)
		tok, _ := auth.GenerateToken(a.Config.Secret, u.ID, u.Username, u.Role)
		return u, tok
	}
	author, authorToken := newUser("sg-author")
	suggester, suggesterToken := newUser("sg-suggester")
	_, outsiderToken := newUser("sg-outsider")
	_, created := h.do(authorToken, http.MethodPost, "/api/v1/books", `{"title":"建议书"}`)
	book := backupData(created)
	bookID := uint(book["id"].(float64))
	a.DB.Create(&models.BookCollaborator{BookID: bookID, UserID: suggester.ID, Role: "suggester", Status: "accepted", InvitedBy: author.ID})
	_, doc := h.do(authorToken, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), `{"title":"第一章","content":"甲\n乙\n丙"}`)
	docID := uint(backupData(doc)["id"].(float64))
	notifications := func(uid uint) int64 {
		var n int64
		a.DB.Model(&models.Notification{}).Where("user_id = ? AND type = ?", uid, "collaboration").Count(&n)
		return n
	}

	// 建议者：可进入写作台、看草稿，不能直接保存
	_, access := h.do(suggesterToken, http.MethodGet, fmt.Sprintf("/api/v1/books/slug/%s/access", book["slug"]), "")
	if backupData(access)["can_suggest"] != true || backupData(access)["can_edit_content"] != false {
		t.Fatalf("建议者权限不对: %v", access)
	}
	if status, _ := h.do(suggesterToken, http.MethodGet, fmt.Sprintf("/api/v1/documents/%d", docID), ""); status != http.StatusOK {
		t.Fatalf("建议者应能读草稿章节: %d", status)
	}
	if _, p := h.do(suggesterToken, http.MethodGet, fmt.Sprintf("/api/v1/books/%d/documents", bookID), ""); len(p["data"].([]any)) != 1 {
		t.Fatalf("建议者的目录应含草稿章节: %v", p)
	}
	if status, _ := h.do(suggesterToken, http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", docID), `{"content":"改"}`); status != http.StatusForbidden {
		t.Fatalf("建议者不能直接保存: %d", status)
	}

	// 提交建议：作者收到通知，目录计数
	base := fmt.Sprintf("/api/v1/documents/%d/suggestions", docID)
	if status, _ := h.do(suggesterToken, http.MethodPost, base, `{"base_title":"第一章","base_content":"甲\n乙\n丙","title":"第一章","content":"甲\n乙\n丙"}`); status != http.StatusBadRequest {
		t.Fatalf("没有修改的建议应拒绝: %d", status)
	}
	status, p := h.do(suggesterToken, http.MethodPost, base, `{"base_title":"第一章","base_content":"甲\n乙\n丙","title":"第一章（修订）","content":"甲\n乙改\n丙","note":"顺了一下"}`)
	if status != http.StatusOK || backupData(p)["status"] != "pending" {
		t.Fatalf("提交建议失败: %d %v", status, p)
	}
	sid := backupData(p)["id"]
	if notifications(author.ID) != 1 {
		t.Fatalf("作者应收到建议通知: %d", notifications(author.ID))
	}
	_, p = h.do(authorToken, http.MethodGet, fmt.Sprintf("/api/v1/books/%d/suggestions/counts", bookID), "")
	if backupData(p)["counts"].(map[string]any)[fmt.Sprint(docID)] != float64(1) {
		t.Fatalf("待处理计数不对: %v", p)
	}
	_, p = h.do(authorToken, http.MethodGet, base, "")
	if items := backupData(p)["items"].([]any); len(items) != 1 || items[0].(map[string]any)["note"] != "顺了一下" || items[0].(map[string]any)["user"].(map[string]any)["username"] != "sg-suggester" {
		t.Fatalf("建议列表不对: %v", p)
	}
	if status, _ := h.do(outsiderToken, http.MethodGet, base, ""); status != http.StatusForbidden {
		t.Fatalf("非协作者不能查看建议: %d", status)
	}

	// 处理：建议者不能自己采纳；作者部分采纳后通知建议者，不能重复处理
	if status, _ := h.do(suggesterToken, http.MethodPost, fmt.Sprintf("/api/v1/suggestions/%v/decide", sid), `{"status":"accepted"}`); status != http.StatusForbidden {
		t.Fatalf("建议者不能处理建议: %d", status)
	}
	if status, p := h.do(authorToken, http.MethodPost, fmt.Sprintf("/api/v1/suggestions/%v/decide", sid), `{"status":"partial"}`); status != http.StatusOK || backupData(p)["status"] != "partial" {
		t.Fatalf("处理建议失败: %d %v", status, p)
	}
	if notifications(suggester.ID) != 1 {
		t.Fatalf("建议者应收到处理结果通知: %d", notifications(suggester.ID))
	}
	if status, _ := h.do(authorToken, http.MethodPost, fmt.Sprintf("/api/v1/suggestions/%v/decide", sid), `{"status":"rejected"}`); status != http.StatusConflict {
		t.Fatalf("不能重复处理: %d", status)
	}
	_, p = h.do(authorToken, http.MethodGet, base+"?status=decided", "")
	if len(backupData(p)["items"].([]any)) != 1 || backupData(p)["pending"].(float64) != 0 {
		t.Fatalf("已处理列表不对: %v", p)
	}

	// 撤回：只能撤回自己未处理的建议
	_, p = h.do(suggesterToken, http.MethodPost, base, `{"base_title":"第一章","base_content":"甲\n乙\n丙","content":"甲\n乙\n丙丁"}`)
	sid2 := backupData(p)["id"]
	if status, _ := h.do(authorToken, http.MethodDelete, fmt.Sprintf("/api/v1/suggestions/%v", sid2), ""); status != http.StatusForbidden {
		t.Fatalf("作者不能撤回别人的建议: %d", status)
	}
	if status, _ := h.do(suggesterToken, http.MethodDelete, fmt.Sprintf("/api/v1/suggestions/%v", sid2), ""); status != http.StatusOK {
		t.Fatalf("撤回建议失败: %d", status)
	}

	// 写作成员与 @ 提及
	_, p = h.do(suggesterToken, http.MethodGet, fmt.Sprintf("/api/v1/books/%d/writer-members", bookID), "")
	members := backupData(p)["items"].([]any)
	if len(members) != 2 || members[0].(map[string]any)["role"] != "owner" || members[1].(map[string]any)["role"] != "suggester" {
		t.Fatalf("写作成员不对: %v", p)
	}
	before := notifications(author.ID)
	if status, _ := h.do(suggesterToken, http.MethodPost, fmt.Sprintf("/api/v1/documents/%d/writer-comments", docID), `{"content":"@sg-author 这里麻烦看一下，@sg-outsider 不是协作者","quote":"乙"}`); status != http.StatusOK {
		t.Fatalf("批注失败: %d", status)
	}
	if got := notifications(author.ID) - before; got != 1 {
		t.Fatalf("被提及的作者应只收到 1 条通知: %d", got)
	}
	var mention models.Notification
	a.DB.Where("user_id = ?", author.ID).Order("id DESC").First(&mention)
	if mention.Title == "" || !(strings.Contains(mention.Title, "提到了你") || strings.Contains(mention.Title, "mentioned you")) {
		t.Fatalf("应为「提到了你」的通知: %q", mention.Title)
	}
	var outsider models.User
	a.DB.Where("username = ?", "sg-outsider").First(&outsider)
	if notifications(outsider.ID) != 0 {
		t.Fatal("不能参与写作的人被 @ 也不应收到通知")
	}
}
