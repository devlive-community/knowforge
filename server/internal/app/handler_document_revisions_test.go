package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"knowforge/server/internal/database"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"

	"github.com/gin-gonic/gin"
)

func TestDocumentRevisionHistoryAndRestore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := database.Open(testdb.Config(t))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("获取数据库连接失败: %v", err)
	}
	defer sqlDB.Close()
	if err := models.All(db); err != nil {
		t.Fatalf("迁移测试数据库失败: %v", err)
	}

	owner := models.User{Username: "revision-owner", Email: "revision-owner@test.local", Role: "user", IsActive: true}
	viewer := models.User{Username: "revision-viewer", Email: "revision-viewer@test.local", Role: "user", IsActive: true}
	editor := models.User{Username: "revision-editor", Email: "revision-editor@test.local", Role: "user", IsActive: true}
	for _, user := range []*models.User{&owner, &viewer, &editor} {
		if err := db.Create(user).Error; err != nil {
			t.Fatalf("创建测试用户失败: %v", err)
		}
	}
	book := models.Book{Title: "版本测试书", Slug: "revision-book", UserID: owner.ID, Status: "draft"}
	if err := db.Create(&book).Error; err != nil {
		t.Fatalf("创建测试书籍失败: %v", err)
	}
	if err := db.Create(&models.BookCollaborator{BookID: book.ID, UserID: editor.ID, Role: "editor"}).Error; err != nil {
		t.Fatalf("创建编辑协作者失败: %v", err)
	}

	a := &App{DB: db}
	requestUser := &owner
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user", requestUser)
		c.Next()
	})
	router.POST("/books/:id/documents", a.CreateDocument)
	router.PUT("/documents/:id", a.UpdateDocument)
	router.GET("/documents/:id/revisions", a.ListDocumentRevisions)
	router.GET("/documents/:id/revisions/:revisionId", a.GetDocumentRevision)
	router.POST("/documents/:id/revisions/:revisionId/restore", a.RestoreDocumentRevision)

	do := func(method, path string, body any) (int, map[string]any) {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, req)
		var payload map[string]any
		_ = json.Unmarshal(recorder.Body.Bytes(), &payload)
		return recorder.Code, payload
	}

	status, createdPayload := do(http.MethodPost, "/books/"+strconv.FormatUint(uint64(book.ID), 10)+"/documents", map[string]any{
		"title": "第一章", "content": "初始正文", "status": "draft",
	})
	if status != http.StatusOK {
		t.Fatalf("创建章节失败: %d %v", status, createdPayload)
	}
	docID := uint(createdPayload["data"].(map[string]any)["id"].(float64))
	var initial models.DocumentRevision
	if err := db.Where("document_id = ?", docID).First(&initial).Error; err != nil {
		t.Fatalf("新建章节未生成初始版本: %v", err)
	}
	if initial.Reason != "create" || initial.Content != "初始正文" {
		t.Fatalf("初始版本内容不正确: %+v", initial)
	}

	status, _ = do(http.MethodPut, "/documents/"+strconv.FormatUint(uint64(docID), 10), map[string]any{
		"title": "第一章（修订）", "content": "第二版正文", "status": "published",
		"create_revision": true, "revision_reason": "publish",
	})
	if status != http.StatusOK {
		t.Fatalf("手动发布章节失败: %d", status)
	}
	status, _ = do(http.MethodPut, "/documents/"+strconv.FormatUint(uint64(docID), 10), map[string]any{"sort_order": 3})
	if status != http.StatusOK {
		t.Fatalf("更新章节排序失败: %d", status)
	}

	status, listPayload := do(http.MethodGet, "/documents/"+strconv.FormatUint(uint64(docID), 10)+"/revisions", nil)
	if status != http.StatusOK {
		t.Fatalf("查询版本列表失败: %d %v", status, listPayload)
	}
	listData := listPayload["data"].(map[string]any)
	if listData["total"].(float64) != 2 {
		t.Fatalf("版本数量错误: %v", listData)
	}
	items := listData["items"].([]any)
	latest := items[0].(map[string]any)
	if latest["reason"] != "publish" || latest["content"] != nil {
		t.Fatalf("列表应包含发布原因且不返回正文: %v", latest)
	}

	status, detailPayload := do(http.MethodGet, "/documents/"+strconv.FormatUint(uint64(docID), 10)+"/revisions/"+strconv.FormatUint(uint64(initial.ID), 10), nil)
	if status != http.StatusOK || detailPayload["data"].(map[string]any)["content"] != "初始正文" {
		t.Fatalf("版本详情错误: %d %v", status, detailPayload)
	}
	status, otherDocPayload := do(http.MethodPost, "/books/"+strconv.FormatUint(uint64(book.ID), 10)+"/documents", map[string]any{
		"title": "第二章", "content": "其他正文", "status": "draft",
	})
	if status != http.StatusOK {
		t.Fatalf("创建第二个章节失败: %d %v", status, otherDocPayload)
	}
	otherDocID := uint(otherDocPayload["data"].(map[string]any)["id"].(float64))
	var otherRevision models.DocumentRevision
	if err := db.Where("document_id = ?", otherDocID).First(&otherRevision).Error; err != nil {
		t.Fatalf("查询第二个章节版本失败: %v", err)
	}
	status, _ = do(http.MethodGet, "/documents/"+strconv.FormatUint(uint64(docID), 10)+"/revisions/"+strconv.FormatUint(uint64(otherRevision.ID), 10), nil)
	if status != http.StatusNotFound {
		t.Fatalf("不得跨章节读取版本: %d", status)
	}

	status, restorePayload := do(http.MethodPost, "/documents/"+strconv.FormatUint(uint64(docID), 10)+"/revisions/"+strconv.FormatUint(uint64(initial.ID), 10)+"/restore", nil)
	if status != http.StatusOK {
		t.Fatalf("恢复版本失败: %d %v", status, restorePayload)
	}
	restored := restorePayload["data"].(map[string]any)
	if restored["content"] != "初始正文" || restored["title"] != "第一章" || restored["status"] != "draft" {
		t.Fatalf("恢复后的章节错误: %v", restored)
	}
	var reasons []string
	if err := db.Model(&models.DocumentRevision{}).Where("document_id = ?", docID).Order("id ASC").Pluck("reason", &reasons).Error; err != nil {
		t.Fatalf("查询恢复快照失败: %v", err)
	}
	if len(reasons) != 4 || reasons[2] != "pre_restore" || reasons[3] != "restore" {
		t.Fatalf("恢复前后快照不完整: %v", reasons)
	}

	requestUser = &viewer
	status, _ = do(http.MethodGet, "/documents/"+strconv.FormatUint(uint64(docID), 10)+"/revisions", nil)
	if status != http.StatusNotFound {
		t.Fatalf("普通读者不应读取版本历史: %d", status)
	}
	requestUser = &editor
	status, _ = do(http.MethodGet, "/documents/"+strconv.FormatUint(uint64(docID), 10)+"/revisions", nil)
	if status != http.StatusOK {
		t.Fatalf("editor 协作者应能读取版本历史: %d", status)
	}
}
