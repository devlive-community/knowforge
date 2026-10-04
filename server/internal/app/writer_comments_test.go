package app

import (
	"fmt"
	"net/http"
	"testing"

	"knowforge/server/internal/auth"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// 写作批注：协作者批注正文、回复、修改、标记解决与删除；通知书籍作者与讨论中的其他人；非协作者不可见；
// 批注变化推送给同书的写作台。
func TestWriterComments(t *testing.T) {
	a, h := newBackupTestApp(t, t.TempDir())
	h.do("", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"批注"},"admin":{"username":"cm-admin","email":"cm-admin@test.local","password":"secret123"}}`)
	newUser := func(name string) (*models.User, string) {
		u := &models.User{Username: name, Email: name + "@test.local", Role: "user", IsActive: true, EmailVerified: true}
		a.DB.Create(u)
		tok, _ := auth.GenerateToken(a.Config.Secret, u.ID, u.Username, u.Role)
		return u, tok
	}
	author, authorToken := newUser("cm-author")
	editor, editorToken := newUser("cm-editor")
	_, outsiderToken := newUser("cm-outsider")
	_, created := h.do(authorToken, http.MethodPost, "/api/v1/books", `{"title":"批注书"}`)
	bookID := uint(backupData(created)["id"].(float64))
	a.DB.Create(&models.BookCollaborator{BookID: bookID, UserID: editor.ID, Role: "editor", Status: "accepted", InvitedBy: author.ID})
	_, doc := h.do(authorToken, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), `{"title":"第一章","content":"这里有一句需要讨论的话。"}`)
	docID := uint(backupData(doc)["id"].(float64))
	base := fmt.Sprintf("/api/v1/documents/%d/writer-comments", docID)
	events, _ := openCollabStream(t, h, authorToken, bookID, docID)

	// 协作者批注一段原文：作者收到通知，写作台收到 comments 事件
	status, p := h.do(editorToken, http.MethodPost, base, `{"content":"这句是否太长？","quote":"需要讨论的话","prefix":"这里有一句","suffix":"。","quote_offset":5}`)
	if status != http.StatusOK || backupData(p)["quote"] != "需要讨论的话" || backupData(p)["user"].(map[string]any)["username"] != "cm-editor" {
		t.Fatalf("创建批注失败: %d %v", status, p)
	}
	rootID := backupData(p)["id"]
	waitEvent(t, events, "comments 事件", func(ev collabEvent) bool { return ev.Name == "comments" && ev.Data["doc_id"] == float64(docID) })
	var n int64
	a.DB.Model(&models.Notification{}).Where("user_id = ? AND type = ?", author.ID, "collaboration").Count(&n)
	if n != 1 {
		t.Fatalf("作者应收到批注通知: %d", n)
	}
	if status, _ := h.do(outsiderToken, http.MethodGet, base, ""); status != http.StatusForbidden {
		t.Fatalf("非协作者不能查看批注: %d", status)
	}
	if status, _ := h.do(outsiderToken, http.MethodPost, base, `{"content":"x"}`); status != http.StatusForbidden {
		t.Fatalf("非协作者不能批注: %d", status)
	}
	if status, _ := h.do(editorToken, http.MethodPost, base, `{"content":"  "}`); status != http.StatusBadRequest {
		t.Fatalf("空批注应拒绝: %d", status)
	}

	// 作者回复：协作者收到回复通知
	status, p = h.do(authorToken, http.MethodPost, base, fmt.Sprintf(`{"content":"拆成两句吧","parent_id":%v}`, rootID))
	if status != http.StatusOK {
		t.Fatalf("回复失败: %d %v", status, p)
	}
	replyID := backupData(p)["id"]
	a.DB.Model(&models.Notification{}).Where("user_id = ? AND type = ?", editor.ID, "collaboration").Count(&n)
	if n != 1 {
		t.Fatalf("协作者应收到回复通知: %d", n)
	}
	_, p = h.do(editorToken, http.MethodGet, base, "")
	items := backupData(p)["items"].([]any)
	if len(items) != 1 || len(items[0].(map[string]any)["replies"].([]any)) != 1 || backupData(p)["open"].(float64) != 1 {
		t.Fatalf("应有 1 个讨论串、1 条回复: %v", p)
	}
	_, p = h.do(editorToken, http.MethodGet, fmt.Sprintf("/api/v1/books/%d/writer-comments/counts", bookID), "")
	if backupData(p)["counts"].(map[string]any)[fmt.Sprint(docID)] != float64(1) {
		t.Fatalf("目录计数不对: %v", p)
	}

	// 修改只能改自己的
	if status, _ := h.do(authorToken, http.MethodPut, fmt.Sprintf("/api/v1/writer-comments/%v", rootID), `{"content":"改"}`); status != http.StatusForbidden {
		t.Fatalf("不能修改别人的批注: %d", status)
	}
	if status, p := h.do(editorToken, http.MethodPut, fmt.Sprintf("/api/v1/writer-comments/%v", rootID), `{"content":"这句话是否太长？"}`); status != http.StatusOK || backupData(p)["content"] != "这句话是否太长？" {
		t.Fatalf("修改自己的批注失败: %d %v", status, p)
	}

	// 标记解决：从未解决列表中移到已解决
	if status, p := h.do(authorToken, http.MethodPost, fmt.Sprintf("/api/v1/writer-comments/%v/resolve", rootID), `{"resolved":true}`); status != http.StatusOK || backupData(p)["resolved"] != true {
		t.Fatalf("标记解决失败: %d %v", status, p)
	}
	if status, _ := h.do(authorToken, http.MethodPost, fmt.Sprintf("/api/v1/writer-comments/%v/resolve", replyID), `{"resolved":true}`); status != http.StatusBadRequest {
		t.Fatalf("回复不能单独标记解决: %d", status)
	}
	_, p = h.do(editorToken, http.MethodGet, base, "")
	if len(backupData(p)["items"].([]any)) != 0 || backupData(p)["resolved"].(float64) != 1 {
		t.Fatalf("解决后不应出现在未解决列表: %v", p)
	}
	_, p = h.do(editorToken, http.MethodGet, base+"?status=resolved", "")
	if len(backupData(p)["items"].([]any)) != 1 {
		t.Fatalf("已解决列表应有 1 条: %v", p)
	}

	// 删除：协作者不能删作者的回复；书籍所有者可以删除任何批注，删除顶级批注连同回复
	if status, _ := h.do(editorToken, http.MethodDelete, fmt.Sprintf("/api/v1/writer-comments/%v", replyID), ""); status != http.StatusForbidden {
		t.Fatalf("协作者不能删除别人的批注: %d", status)
	}
	if status, _ := h.do(authorToken, http.MethodDelete, fmt.Sprintf("/api/v1/writer-comments/%v", rootID), ""); status != http.StatusOK {
		t.Fatalf("所有者删除批注失败: %d", status)
	}
	var left int64
	a.DB.Model(&models.WriterComment{}).Where("document_id = ?", docID).Count(&left)
	if left != 0 {
		t.Fatalf("删除顶级批注应连同回复: 剩 %d 条", left)
	}
}
