package app

import (
	"fmt"
	"net/http"
	"testing"

	"knowforge/server/internal/auth"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

func TestLineChanges(t *testing.T) {
	cases := []struct {
		before, after  string
		added, removed int
	}{
		{"a\nb\nc", "a\nb\nc", 0, 0},
		{"a\nb\nc", "a\nB\nc\nd", 2, 1},
		{"", "x", 1, 1},
		{"a\na\nb", "a\nb", 0, 1},
	}
	for _, tc := range cases {
		if added, removed := lineChanges(tc.before, tc.after); added != tc.added || removed != tc.removed {
			t.Fatalf("lineChanges(%q, %q) = +%d -%d, want +%d -%d", tc.before, tc.after, added, removed, tc.added, tc.removed)
		}
	}
}

// 协作动态：新建章节、保存（同一人短时间内连续保存合并为一条并累加行数）、批注、分工都记入动态；可按章节筛选；
// 只有能参与写作的人可以查看。
func TestWriterActivity(t *testing.T) {
	a, h := newBackupTestApp(t, t.TempDir())
	h.do("", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"动态"},"admin":{"username":"ac-admin","email":"ac-admin@test.local","password":"secret123"}}`)
	newUser := func(name string) (*models.User, string) {
		u := &models.User{Username: name, Email: name + "@test.local", Role: "user", IsActive: true, EmailVerified: true}
		a.DB.Create(u)
		tok, _ := auth.GenerateToken(a.Config.Secret, u.ID, u.Username, u.Role)
		return u, tok
	}
	author, authorToken := newUser("ac-author")
	editor, editorToken := newUser("ac-editor")
	_, outsiderToken := newUser("ac-outsider")
	_, created := h.do(authorToken, http.MethodPost, "/api/v1/books", `{"title":"动态书"}`)
	bookID := uint(backupData(created)["id"].(float64))
	a.DB.Create(&models.BookCollaborator{BookID: bookID, UserID: editor.ID, Role: "editor", Status: "accepted", InvitedBy: author.ID})
	_, d1 := h.do(authorToken, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), `{"title":"第一章","content":"甲"}`)
	doc1 := uint(backupData(d1)["id"].(float64))
	_, d2 := h.do(authorToken, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), `{"title":"第二章"}`)
	doc2 := uint(backupData(d2)["id"].(float64))

	h.do(editorToken, http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", doc1), `{"content":"甲\n乙"}`)
	h.do(editorToken, http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", doc1), `{"content":"甲\n乙\n丙"}`)
	h.do(editorToken, http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", doc1), `{"sort_order":5}`) // 只调整排序不算保存
	h.do(editorToken, http.MethodPost, fmt.Sprintf("/api/v1/documents/%d/writer-comments", doc1), `{"content":"这里再看看","quote":"乙"}`)
	h.do(authorToken, http.MethodPut, fmt.Sprintf("/api/v1/documents/%d/task", doc2), fmt.Sprintf(`{"assignee_id":%d,"stage":"writing"}`, editor.ID))

	url := fmt.Sprintf("/api/v1/books/%d/writer-activity", bookID)
	_, p := h.do(editorToken, http.MethodGet, url, "")
	items := backupData(p)["items"].([]any)
	kinds := []string{}
	for _, it := range items {
		kinds = append(kinds, it.(map[string]any)["kind"].(string))
	}
	want := []string{"task.updated", "comment.created", "doc.saved", "doc.created", "doc.created"}
	if fmt.Sprint(kinds) != fmt.Sprint(want) {
		t.Fatalf("动态不对: %v", kinds)
	}
	saved := items[2].(map[string]any)
	if d := saved["detail"].(map[string]any); d["added"].(float64) != 2 || d["removed"].(float64) != 0 || saved["user"].(map[string]any)["username"] != "ac-editor" {
		t.Fatalf("连续保存应合并并累加行数: %v", saved)
	}
	if d := items[0].(map[string]any)["detail"].(map[string]any); d["assignee"] != "ac-editor" || d["stage"] != "writing" || d["title"] != "第二章" {
		t.Fatalf("分工动态不对: %v", d)
	}
	_, p = h.do(authorToken, http.MethodGet, url+fmt.Sprintf("?doc_id=%d", doc2), "")
	if n := len(backupData(p)["items"].([]any)); n != 2 {
		t.Fatalf("按章节筛选应有 2 条: %d", n)
	}
	if status, _ := h.do(outsiderToken, http.MethodGet, url, ""); status != http.StatusForbidden {
		t.Fatalf("非协作者不能查看动态: %d", status)
	}
}
