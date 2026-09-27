package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

func TestReaderRetentionCohorts(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("创建应用失败: %v", err)
	}
	server := httptest.NewServer(a.Router())
	defer server.Close()
	client := &http.Client{Timeout: 10 * time.Second}

	request := func(method, path string, body any, token string) (int, map[string]any) {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		payload := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return resp.StatusCode, payload
	}

	_, installed := request(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "留存测试站"},
		"admin":    map[string]any{"username": "author", "email": "author@test.local", "password": "secret123"},
	}, "")
	authorToken := installed["data"].(map[string]any)["token"].(string)

	_, createdBook := request(http.MethodPost, "/api/v1/books", map[string]any{
		"title": "Retention Book", "status": "published", "is_public": true,
	}, authorToken)
	bookID := uint(createdBook["data"].(map[string]any)["id"].(float64))
	docIDs := []uint{}
	for i := 0; i < 4; i++ {
		_, doc := request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), map[string]any{
			"title": fmt.Sprintf("Chapter %d", i+1), "content": "content", "status": "published",
		}, authorToken)
		docIDs = append(docIDs, uint(doc["data"].(map[string]any)["id"].(float64)))
	}

	// 队列周 = 当前周往前 2 周（窗口内索引 9），可观测偏移 0/1/2。
	cohortWeek := weekStart(currentTime()).AddDate(0, 0, -7*2)
	seed := func(userID, docID uint, at time.Time) {
		if err := a.DB.Create(&models.ReadChapter{UserID: userID, BookID: bookID, DocID: docID, CreatedAt: at}).Error; err != nil {
			t.Fatalf("写入阅读记录失败: %v", err)
		}
	}
	// 读者 9001：首读在偏移 0，另一次在偏移 1。
	seed(9001, docIDs[0], cohortWeek.AddDate(0, 0, 1))
	seed(9001, docIDs[1], cohortWeek.AddDate(0, 0, 8))
	// 读者 9002：仅偏移 0。
	seed(9002, docIDs[2], cohortWeek.AddDate(0, 0, 2))

	status, response := request(http.MethodGet, "/api/v1/users/me/reader-retention", nil, authorToken)
	if status != http.StatusOK {
		t.Fatalf("读取留存失败: %d %v", status, response)
	}
	data := response["data"].(map[string]any)
	if data["weeks"] != float64(readerRetentionWeeks) {
		t.Fatalf("周数错误: %v", data["weeks"])
	}
	cohorts := data["cohorts"].([]any)
	if len(cohorts) != readerRetentionWeeks {
		t.Fatalf("队列数应为 %d: %d", readerRetentionWeeks, len(cohorts))
	}

	target := cohortWeek.Format("2006-01-02")
	var found map[string]any
	for _, c := range cohorts {
		row := c.(map[string]any)
		if row["week"] == target {
			found = row
			break
		}
	}
	if found == nil {
		t.Fatalf("未找到队列周 %s", target)
	}
	if found["size"] != float64(2) {
		t.Fatalf("队列人数错误: %v", found["size"])
	}
	retention := found["retention"].([]any)
	if len(retention) != 3 || retention[0] != float64(2) || retention[1] != float64(1) || retention[2] != float64(0) {
		t.Fatalf("留存明细错误: %v", retention)
	}

	curve := data["curve"].([]any)
	if curve[0] != float64(100) {
		t.Fatalf("曲线偏移 0 应为 100%%: %v", curve[0])
	}
	if curve[1] != float64(50) {
		t.Fatalf("曲线偏移 1 应为 50%%: %v", curve[1])
	}

	// 无书籍的读者留存为空骨架。
	_, registered := request(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "reader", "email": "reader@test.local", "password": "secret123",
	}, "")
	readerToken := registered["data"].(map[string]any)["token"].(string)
	status, empty := request(http.MethodGet, "/api/v1/users/me/reader-retention", nil, readerToken)
	if status != http.StatusOK {
		t.Fatalf("空留存应成功: %d %v", status, empty)
	}
	emptyData := empty["data"].(map[string]any)
	if len(emptyData["cohorts"].([]any)) != readerRetentionWeeks {
		t.Fatalf("空留存仍应返回骨架队列")
	}
	if emptyData["curve"].([]any)[0] != nil {
		t.Fatalf("空留存曲线应为 null: %v", emptyData["curve"].([]any)[0])
	}
}
