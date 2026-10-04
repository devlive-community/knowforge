package app

import (
	"fmt"
	"net/http"
	"testing"

	"knowforge/server/internal/auth"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// 章节分工：能编辑的人设置负责人、阶段与截止日期；负责人须是书籍成员；被分配者收到通知，进入「待审阅」时通知作者与编辑者；
// 建议者可以查看但不能设置；非协作者不能查看。
func TestWriterTasks(t *testing.T) {
	a, h := newBackupTestApp(t, t.TempDir())
	h.do("", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"分工"},"admin":{"username":"tk-admin","email":"tk-admin@test.local","password":"secret123"}}`)
	newUser := func(name string) (*models.User, string) {
		u := &models.User{Username: name, Email: name + "@test.local", Role: "user", IsActive: true, EmailVerified: true}
		a.DB.Create(u)
		tok, _ := auth.GenerateToken(a.Config.Secret, u.ID, u.Username, u.Role)
		return u, tok
	}
	author, authorToken := newUser("tk-author")
	editor, editorToken := newUser("tk-editor")
	suggester, suggesterToken := newUser("tk-suggester")
	outsider, outsiderToken := newUser("tk-outsider")
	_, created := h.do(authorToken, http.MethodPost, "/api/v1/books", `{"title":"分工书"}`)
	bookID := uint(backupData(created)["id"].(float64))
	a.DB.Create(&models.BookCollaborator{BookID: bookID, UserID: editor.ID, Role: "editor", Status: "accepted", InvitedBy: author.ID})
	a.DB.Create(&models.BookCollaborator{BookID: bookID, UserID: suggester.ID, Role: "suggester", Status: "accepted", InvitedBy: author.ID})
	_, doc := h.do(authorToken, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), `{"title":"第一章"}`)
	docID := uint(backupData(doc)["id"].(float64))
	taskURL := fmt.Sprintf("/api/v1/documents/%d/task", docID)
	notifications := func(uid uint) int64 {
		var n int64
		a.DB.Model(&models.Notification{}).Where("user_id = ? AND type = ?", uid, "collaboration").Count(&n)
		return n
	}

	status, p := h.do(editorToken, http.MethodPut, taskURL, fmt.Sprintf(`{"assignee_id":%d,"stage":"writing","due_at":"2026-12-01"}`, suggester.ID))
	if status != http.StatusOK || backupData(p)["stage"] != "writing" || backupData(p)["assignee"].(map[string]any)["username"] != "tk-suggester" || backupData(p)["due_at"] == nil {
		t.Fatalf("设置分工失败: %d %v", status, p)
	}
	if notifications(suggester.ID) != 1 {
		t.Fatalf("被分配者应收到通知: %d", notifications(suggester.ID))
	}
	_, p = h.do(suggesterToken, http.MethodGet, fmt.Sprintf("/api/v1/books/%d/chapter-tasks", bookID), "")
	if items := backupData(p)["items"].([]any); len(items) != 1 || items[0].(map[string]any)["document_id"] != float64(docID) {
		t.Fatalf("建议者应能查看分工: %v", p)
	}
	if status, _ := h.do(suggesterToken, http.MethodPut, taskURL, `{"stage":"done"}`); status != http.StatusForbidden {
		t.Fatalf("建议者不能设置分工: %d", status)
	}
	if status, _ := h.do(outsiderToken, http.MethodGet, fmt.Sprintf("/api/v1/books/%d/chapter-tasks", bookID), ""); status != http.StatusForbidden {
		t.Fatalf("非协作者不能查看分工: %d", status)
	}
	for _, body := range []string{fmt.Sprintf(`{"assignee_id":%d}`, outsider.ID), `{"stage":"unknown"}`, `{"due_at":"12/01/2026"}`} {
		if status, _ := h.do(editorToken, http.MethodPut, taskURL, body); status != http.StatusBadRequest {
			t.Fatalf("应拒绝无效参数 %s: %d", body, status)
		}
	}

	// 进入待审阅：通知作者（编辑者自己操作不通知自己）
	before := notifications(author.ID)
	if status, _ := h.do(editorToken, http.MethodPut, taskURL, `{"stage":"review"}`); status != http.StatusOK {
		t.Fatalf("更新阶段失败: %d", status)
	}
	if notifications(author.ID)-before != 1 || notifications(editor.ID) != 0 {
		t.Fatalf("待审阅通知不对: 作者 %d 编辑者 %d", notifications(author.ID)-before, notifications(editor.ID))
	}
	// 取消负责人与截止日期，只改动传入的字段
	status, p = h.do(authorToken, http.MethodPut, taskURL, `{"assignee_id":0,"due_at":null}`)
	if status != http.StatusOK || backupData(p)["assignee"] != nil || backupData(p)["due_at"] != nil || backupData(p)["stage"] != "review" {
		t.Fatalf("取消负责人与截止日期失败: %d %v", status, p)
	}
}
