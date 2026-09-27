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

func TestReadingAnnotationsPrivacyAndVisibility(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
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

	_, installed := request(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t), "site": map[string]any{"name": "标注测试"},
		"admin": map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	adminToken := installed["data"].(map[string]any)["token"].(string)
	_, createdBook := request(http.MethodPost, "/api/v1/books", map[string]any{
		"title": "Public Book", "status": "published", "is_public": true,
	}, adminToken)
	book := createdBook["data"].(map[string]any)
	bookID := int(book["id"].(float64))
	_, createdDoc := request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), map[string]any{
		"title": "Chapter", "content": "A selected paragraph for annotation.", "status": "published",
	}, adminToken)
	doc := createdDoc["data"].(map[string]any)
	docID := int(doc["id"].(float64))

	register := func(username string) string {
		_, response := request(http.MethodPost, "/api/v1/auth/register", map[string]any{
			"username": username, "email": username + "@test.local", "password": "secret123",
		}, "")
		return response["data"].(map[string]any)["token"].(string)
	}
	readerToken, otherToken := register("reader"), register("other")

	status, created := request(http.MethodPost, fmt.Sprintf("/api/v1/documents/%d/annotations", docID), map[string]any{
		"kind": "note", "color": "yellow", "note": "private thought", "quote": "selected paragraph",
		"prefix": "A ", "suffix": " for annotation.", "start_offset": 2, "end_offset": 20,
	}, readerToken)
	if status != http.StatusOK {
		t.Fatalf("创建私人笔记失败: %d %v", status, created)
	}
	annotationID := int(created["data"].(map[string]any)["id"].(float64))
	status, updated := request(http.MethodPut, fmt.Sprintf("/api/v1/annotations/%d", annotationID), map[string]any{
		"note": "updated private thought", "start_offset": 9, "end_offset": 27, "anchor_status": "relocated",
	}, readerToken)
	if status != http.StatusOK || updated["data"].(map[string]any)["anchor_status"] != "relocated" {
		t.Fatalf("更新自己的私人笔记失败: %d %v", status, updated)
	}

	for range 2 {
		status, _ = request(http.MethodPost, fmt.Sprintf("/api/v1/documents/%d/annotations", docID), map[string]any{"kind": "bookmark"}, readerToken)
		if status != http.StatusOK {
			t.Fatalf("创建章节书签失败: %d", status)
		}
	}
	_, listed := request(http.MethodGet, fmt.Sprintf("/api/v1/documents/%d/annotations", docID), nil, readerToken)
	items := listed["data"].([]any)
	if len(items) != 2 {
		t.Fatalf("重复章节书签必须覆盖: %v", items)
	}
	status, aggregate := request(http.MethodGet, "/api/v1/users/me/annotations?kind=note", nil, readerToken)
	if status != http.StatusOK || aggregate["data"].(map[string]any)["total"] != float64(1) {
		t.Fatalf("我的笔记聚合错误: %d %v", status, aggregate)
	}

	status, _ = request(http.MethodPut, fmt.Sprintf("/api/v1/annotations/%d", annotationID), map[string]any{"note": "stolen"}, otherToken)
	if status != http.StatusNotFound {
		t.Fatalf("其他用户不得修改私人笔记: %d", status)
	}
	status, _ = request(http.MethodGet, fmt.Sprintf("/api/v1/documents/%d/annotations", docID), nil, "")
	if status != http.StatusUnauthorized {
		t.Fatalf("匿名用户不得读取私人标注: %d", status)
	}

	request(http.MethodPut, fmt.Sprintf("/api/v1/books/%d", bookID), map[string]any{"is_public": false}, adminToken)
	status, aggregate = request(http.MethodGet, "/api/v1/users/me/annotations", nil, readerToken)
	if status != http.StatusOK || aggregate["data"].(map[string]any)["total"] != float64(0) {
		t.Fatalf("权限撤销后聚合页不得暴露资源: %d %v", status, aggregate)
	}
	status, _ = request(http.MethodGet, fmt.Sprintf("/api/v1/documents/%d/annotations", docID), nil, readerToken)
	if status != http.StatusNotFound {
		t.Fatalf("权限撤销后章节标注接口必须隐藏资源: %d", status)
	}
	status, _ = request(http.MethodDelete, fmt.Sprintf("/api/v1/annotations/%d", annotationID), nil, readerToken)
	if status != http.StatusOK {
		t.Fatalf("权限撤销后仍应允许用户删除自己的私人数据: %d", status)
	}
}
