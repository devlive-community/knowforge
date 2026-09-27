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

func TestBookAnalyticsAggregationAndAuthorization(t *testing.T) {
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
		"site":     map[string]any{"name": "分析测试站"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	adminToken := installed["data"].(map[string]any)["token"].(string)

	_, createdBook := request(http.MethodPost, "/api/v1/books", map[string]any{
		"title": "Analytics Book", "status": "published", "is_public": true,
	}, adminToken)
	bookID := int(createdBook["data"].(map[string]any)["id"].(float64))
	createDocument := func(title string) map[string]any {
		_, response := request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), map[string]any{
			"title": title, "content": "content", "status": "published",
		}, adminToken)
		return response["data"].(map[string]any)
	}
	firstDoc := createDocument("First")
	secondDoc := createDocument("Second")
	firstDocID := int(firstDoc["id"].(float64))
	secondDocID := int(secondDoc["id"].(float64))

	request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/view", bookID), map[string]any{"referrer": "https://www.google.com/search?q=book"}, "")
	request(http.MethodPost, fmt.Sprintf("/api/v1/documents/%d/view", firstDocID), map[string]any{"referrer": server.URL + "/book/detail"}, "")
	request(http.MethodPost, fmt.Sprintf("/api/v1/documents/%d/view", firstDocID), map[string]any{"referrer": "https://example.com/article"}, "")

	status, response := request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d/analytics?days=7", bookID), nil, adminToken)
	if status != http.StatusOK {
		t.Fatalf("作者读取分析失败: %d %v", status, response)
	}
	data := response["data"].(map[string]any)
	if data["period_views"] != float64(3) || data["lifetime_views"] != float64(3) {
		t.Fatalf("浏览聚合错误: %v", data)
	}
	if trend := data["trend"].([]any); len(trend) != 7 {
		t.Fatalf("7 天趋势必须补齐自然日: %v", trend)
	}
	popular := data["popular_chapters"].([]any)
	if len(popular) != 1 || popular[0].(map[string]any)["view_count"] != float64(2) {
		t.Fatalf("热门章节聚合错误: %v", popular)
	}
	sources := data["sources"].([]any)
	if len(sources) != 3 {
		t.Fatalf("应聚合搜索、站内和外部三类来源: %v", sources)
	}

	_, registered := request(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "reader", "email": "reader@test.local", "password": "secret123",
	}, "")
	readerToken := registered["data"].(map[string]any)["token"].(string)
	for _, doc := range []struct {
		id    int
		slug  string
		title string
	}{
		{firstDocID, firstDoc["slug"].(string), "First"},
		{secondDocID, secondDoc["slug"].(string), "Second"},
	} {
		request(http.MethodPut, fmt.Sprintf("/api/v1/reading-progress/%d", bookID), map[string]any{
			"doc_id": doc.id, "doc_slug": doc.slug, "doc_title": doc.title,
		}, readerToken)
	}
	_, completed := request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d/analytics?days=7", bookID), nil, adminToken)
	completion := completed["data"].(map[string]any)
	if completion["registered_readers"] != float64(1) || completion["completed_readers"] != float64(1) || completion["completion_rate"] != float64(100) {
		t.Fatalf("阅读完成率计算错误: %v", completion)
	}

	status, hidden := request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d/analytics?days=7", bookID), nil, readerToken)
	if status != http.StatusNotFound || hidden["success"] != false {
		t.Fatalf("普通读者不应访问作者分析: %d %v", status, hidden)
	}

	boundaryDay := analyticsDayStart(currentTime()).AddDate(0, 0, -(bookAnalyticsRetentionDays - 1)).Format("2006-01-02")
	expiredDay := analyticsDayStart(currentTime()).AddDate(0, 0, -bookAnalyticsRetentionDays).Format("2006-01-02")
	for _, day := range []string{boundaryDay, expiredDay} {
		if err := a.DB.Create(&models.BookAnalyticsDaily{
			BookID: uint(bookID), DocumentID: uint(secondDocID), Day: day, Source: "direct", ViewCount: 1,
		}).Error; err != nil {
			t.Fatalf("写入留存测试数据失败: %v", err)
		}
	}
	if err := purgeExpiredBookAnalytics(a.DB); err != nil {
		t.Fatalf("清理过期分析数据失败: %v", err)
	}
	var retained, expired int64
	a.DB.Model(&models.BookAnalyticsDaily{}).Where("book_id = ? AND day = ?", bookID, boundaryDay).Count(&retained)
	a.DB.Model(&models.BookAnalyticsDaily{}).Where("book_id = ? AND day = ?", bookID, expiredDay).Count(&expired)
	if retained != 1 || expired != 0 {
		t.Fatalf("180 天留存边界错误: retained=%d expired=%d", retained, expired)
	}
}

func TestClassifyAnalyticsSource(t *testing.T) {
	cases := map[string]string{
		"":                               "direct",
		"not a url":                      "direct",
		"https://knowforge.test/book":    "internal",
		"https://www.baidu.com/s?wd=x":   "search",
		"https://www.zhihu.com/question": "social",
		"https://example.com/article":    "external",
	}
	for referrer, expected := range cases {
		if actual := classifyAnalyticsSource(referrer, "knowforge.test:6969"); actual != expected {
			t.Errorf("来源分类 %q: got=%s want=%s", referrer, actual, expected)
		}
	}
}
