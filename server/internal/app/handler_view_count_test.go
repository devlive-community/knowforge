package app

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"knowforge/server/internal/database"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"

	"github.com/gin-gonic/gin"
)

// TestConcurrentViewCountIncrements 验证浏览量使用数据库原子自增，并发请求不会丢更新。
func TestConcurrentViewCountIncrements(t *testing.T) {
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
	sqlDB.SetMaxOpenConns(16)

	if err := models.All(db); err != nil {
		t.Fatalf("迁移测试数据库失败: %v", err)
	}
	user := models.User{Username: "view-owner", Email: "view-owner@test.local", IsActive: true}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("创建测试用户失败: %v", err)
	}
	book := models.Book{
		Title: "并发浏览量测试", Slug: "concurrent-views", UserID: user.ID,
		Status: "published", IsPublic: true,
	}
	if err := db.Create(&book).Error; err != nil {
		t.Fatalf("创建测试书籍失败: %v", err)
	}
	doc := models.Document{
		BookID: book.ID, UserID: user.ID, Title: "公开章节", Slug: "public-chapter",
		Status: "published",
	}
	if err := db.Create(&doc).Error; err != nil {
		t.Fatalf("创建测试章节失败: %v", err)
	}

	a := &App{DB: db}
	const requests = 40
	runConcurrentViewRequests(t, requests, book.ID, a.IncrementBookView)
	if err := db.First(&book, book.ID).Error; err != nil {
		t.Fatalf("重新读取书籍失败: %v", err)
	}
	if book.ViewCount != requests {
		t.Fatalf("书籍并发浏览量丢失: got=%d want=%d", book.ViewCount, requests)
	}

	if err := db.Model(&models.Book{}).Where("id = ?", book.ID).UpdateColumn("view_count", 0).Error; err != nil {
		t.Fatalf("重置书籍浏览量失败: %v", err)
	}
	runConcurrentViewRequests(t, requests, doc.ID, a.IncrementDocumentView)
	if err := db.First(&doc, doc.ID).Error; err != nil {
		t.Fatalf("重新读取章节失败: %v", err)
	}
	if err := db.First(&book, book.ID).Error; err != nil {
		t.Fatalf("重新读取书籍失败: %v", err)
	}
	if doc.ViewCount != requests {
		t.Fatalf("章节并发浏览量丢失: got=%d want=%d", doc.ViewCount, requests)
	}
	if book.ViewCount != requests {
		t.Fatalf("章节浏览未完整同步到书籍: got=%d want=%d", book.ViewCount, requests)
	}
}

func runConcurrentViewRequests(t *testing.T, count int, id uint, handler gin.HandlerFunc) {
	t.Helper()
	start := make(chan struct{})
	statuses := make(chan int, count)
	var ready sync.WaitGroup
	var done sync.WaitGroup
	ready.Add(count)
	done.Add(count)

	for i := 0; i < count; i++ {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			ctx.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(uint64(id), 10)}}
			handler(ctx)
			statuses <- recorder.Code
		}()
	}

	ready.Wait()
	close(start)
	done.Wait()
	close(statuses)
	for status := range statuses {
		if status != http.StatusOK {
			t.Fatalf("并发浏览请求返回异常状态: %d", status)
		}
	}
}
