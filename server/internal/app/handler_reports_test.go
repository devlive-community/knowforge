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
	"knowforge/server/internal/testdb"
)

func TestContentReportModerationFlow(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, _ := config.Load()
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Router())
	defer server.Close()
	client := &http.Client{Timeout: 10 * time.Second}
	request := func(method, path string, body any, token string) (int, map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		payload := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return resp.StatusCode, payload
	}

	_, install := request(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t), "site": map[string]any{"name": "举报测试"},
		"admin": map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	adminToken := install["data"].(map[string]any)["token"].(string)
	_, bookResponse := request(http.MethodPost, "/api/v1/books", map[string]any{
		"title": "Reported Book", "status": "published", "is_public": true,
	}, adminToken)
	bookID := int(bookResponse["data"].(map[string]any)["id"].(float64))

	_, register := request(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "reporter", "email": "reporter@test.local", "password": "secret123",
	}, "")
	readerToken := register["data"].(map[string]any)["token"].(string)
	_, privateBookResponse := request(http.MethodPost, "/api/v1/books", map[string]any{
		"title": "Private Book", "status": "draft", "is_public": false,
	}, adminToken)
	privateBookID := int(privateBookResponse["data"].(map[string]any)["id"].(float64))
	status, _ := request(http.MethodPost, "/api/v1/reports", map[string]any{
		"target_type": "book", "target_id": privateBookID, "reason": "other",
	}, readerToken)
	if status != http.StatusNotFound {
		t.Fatalf("无权查看的私有书籍必须对举报人隐藏: %d", status)
	}
	status, created := request(http.MethodPost, "/api/v1/reports", map[string]any{
		"target_type": "book", "target_id": bookID, "reason": "misleading", "description": "The description is misleading.",
	}, readerToken)
	if status != http.StatusOK {
		t.Fatalf("提交举报失败: %d %v", status, created)
	}
	reportID := int(created["data"].(map[string]any)["id"].(float64))
	status, _ = request(http.MethodPost, "/api/v1/reports", map[string]any{
		"target_type": "book", "target_id": bookID, "reason": "spam",
	}, readerToken)
	if status != http.StatusConflict {
		t.Fatalf("重复待处理举报应冲突: %d", status)
	}
	status, _ = request(http.MethodGet, "/api/v1/admin/reports", nil, readerToken)
	if status != http.StatusForbidden {
		t.Fatalf("普通用户不得查看举报人队列: %d", status)
	}
	status, reports := request(http.MethodGet, "/api/v1/admin/reports?status=pending", nil, adminToken)
	items := reports["data"].(map[string]any)["items"].([]any)
	if status != http.StatusOK || len(items) != 1 || items[0].(map[string]any)["reporter_email"] != "reporter@test.local" {
		t.Fatalf("管理员举报队列错误: %d %v", status, reports)
	}
	status, resolved := request(http.MethodPut, fmt.Sprintf("/api/v1/admin/reports/%d", reportID), map[string]any{
		"resolution": "takedown", "note": "Confirmed by moderation.",
	}, adminToken)
	if status != http.StatusOK || resolved["data"].(map[string]any)["status"] != "resolved" {
		t.Fatalf("下架处理失败: %d %v", status, resolved)
	}
	status, _ = request(http.MethodPut, fmt.Sprintf("/api/v1/admin/reports/%d", reportID), map[string]any{
		"resolution": "reject",
	}, adminToken)
	if status != http.StatusConflict {
		t.Fatalf("已处理举报不得重复处理: %d", status)
	}
	status, _ = request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d", bookID), nil, "")
	if status == http.StatusOK {
		t.Fatalf("下架书籍不应继续公开读取: %d", status)
	}
	_, notifications := request(http.MethodGet, "/api/v1/notifications", nil, readerToken)
	notificationItems := notifications["data"].(map[string]any)["notifications"].([]any)
	if len(notificationItems) == 0 || notificationItems[0].(map[string]any)["type"] != "moderation" {
		t.Fatalf("举报处理结果必须通知举报人: %v", notifications)
	}
	_, audit := request(http.MethodGet, "/api/v1/admin/audit-logs?action=report.resolved", nil, adminToken)
	if audit["data"].(map[string]any)["total"] != float64(1) {
		t.Fatalf("举报处理必须写入审计日志: %v", audit)
	}
}
