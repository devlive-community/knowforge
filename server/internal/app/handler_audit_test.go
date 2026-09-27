package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"knowforge/server/internal/config"
	"knowforge/server/internal/testdb"
)

func TestAdminAuditLogs(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(a.Router())
	defer ts.Close()

	client := &http.Client{Timeout: 10 * time.Second}
	request := func(method, path string, body any, token string) (int, map[string]any) {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(raw))
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
		"site":     map[string]any{"name": "审计测试站"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	adminToken := installed["data"].(map[string]any)["token"].(string)
	_, registered := request(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "alice", "email": "alice@test.local", "password": "secret123",
	}, "")
	aliceData := registered["data"].(map[string]any)
	aliceToken := aliceData["token"].(string)
	aliceID := uint64(aliceData["user"].(map[string]any)["id"].(float64))

	if status, _ := request(http.MethodGet, "/api/v1/admin/audit-logs", nil, ""); status != http.StatusUnauthorized {
		t.Fatalf("匿名查看审计日志应 401: %d", status)
	}
	if status, _ := request(http.MethodGet, "/api/v1/admin/audit-logs", nil, aliceToken); status != http.StatusForbidden {
		t.Fatalf("普通用户查看审计日志应 403: %d", status)
	}
	status, empty := request(http.MethodGet, "/api/v1/admin/audit-logs", nil, adminToken)
	items, okItems := empty["data"].(map[string]any)["items"].([]any)
	if status != http.StatusOK || !okItems || items == nil || len(items) != 0 {
		t.Fatalf("空审计日志必须返回 []: %d %v", status, empty)
	}

	if status, payload := request(http.MethodPut, "/api/v1/admin/users/"+strconv.FormatUint(aliceID, 10)+"/role",
		map[string]any{"role": "admin"}, adminToken); status != http.StatusOK {
		t.Fatalf("变更角色失败: %d %v", status, payload)
	}
	const secret = "audit-must-never-store-this-secret"
	if status, payload := request(http.MethodPut, "/api/v1/admin/configs", map[string]any{
		"key": "integration.secret_token", "value": secret, "description": "敏感测试配置",
	}, adminToken); status != http.StatusOK {
		t.Fatalf("保存配置失败: %d %v", status, payload)
	}

	status, filtered := request(http.MethodGet,
		"/api/v1/admin/audit-logs?actor=adm&action=config.updated&resource_type=config", nil, adminToken)
	if status != http.StatusOK {
		t.Fatalf("筛选审计日志失败: %d %v", status, filtered)
	}
	data := filtered["data"].(map[string]any)
	filteredItems := data["items"].([]any)
	if data["total"].(float64) != 1 || len(filteredItems) != 1 {
		t.Fatalf("筛选应仅命中配置变更: %v", data)
	}
	entry := filteredItems[0].(map[string]any)
	if entry["actor_username"] != "admin" || entry["resource_id"] != "integration.secret_token" {
		t.Fatalf("审计主体或资源错误: %v", entry)
	}
	encoded, _ := json.Marshal(filtered)
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("审计响应泄露敏感配置明文: %s", encoded)
	}
	summary := entry["summary"].(map[string]any)
	if len(summary["changed_fields"].([]any)) == 0 {
		t.Fatalf("审计摘要应列出变更字段: %v", summary)
	}

	if status, _ := request(http.MethodGet, "/api/v1/admin/audit-logs?from=09-09-2026", nil, adminToken); status != http.StatusBadRequest {
		t.Fatalf("非法日期应 400: %d", status)
	}
}
