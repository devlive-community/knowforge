package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"knowforge/server/internal/config"
	"knowforge/server/internal/testdb"
)

// 通用系统配置后台集成测试（config:manage，仅管理员）：
//   - 匿名与普通用户访问被拒绝（401/403）
//   - 安装后关键键存在且标记 reserved
//   - 新增/更新自定义配置，非法键被拒
//   - 删除自定义配置成功，删除系统关键键被拒
func TestAdminConfigs(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("创建应用失败: %v", err)
	}
	ts := httptest.NewServer(a.Router())
	defer ts.Close()

	client := &http.Client{Timeout: 10 * 1e9}
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
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return resp.StatusCode, payload
	}

	_, install := request(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "配置测试站"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	adminToken := install["data"].(map[string]any)["token"].(string)

	_, reg := request(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "alice", "email": "alice@test.local", "password": "secret123",
	}, "")
	aliceToken := reg["data"].(map[string]any)["token"].(string)

	// 1. 权限拒绝：匿名 401、普通用户 403
	if status, _ := request(http.MethodGet, "/api/v1/admin/configs", nil, ""); status != http.StatusUnauthorized {
		t.Fatalf("匿名访问配置列表应 401: %d", status)
	}
	if status, _ := request(http.MethodGet, "/api/v1/admin/configs", nil, aliceToken); status != http.StatusForbidden {
		t.Fatalf("普通用户访问配置列表应 403: %d", status)
	}

	// 2. 列表：items 为数组，site_name 存在且 reserved=true
	status, list := request(http.MethodGet, "/api/v1/admin/configs", nil, adminToken)
	if status != http.StatusOK {
		t.Fatalf("管理员列表应 200: %d %v", status, list)
	}
	items, okItems := list["data"].(map[string]any)["items"].([]any)
	if !okItems || items == nil {
		t.Fatalf("配置 items 必须为数组: %v", list["data"])
	}
	foundReserved := false
	for _, it := range items {
		m := it.(map[string]any)
		if m["key"] == "site_name" && m["reserved"] == true {
			foundReserved = true
		}
	}
	if !foundReserved {
		t.Fatalf("site_name 应存在且标记为 reserved")
	}

	// 3. 新增自定义配置
	if status, _ := request(http.MethodPut, "/api/v1/admin/configs",
		map[string]any{"key": "feature.beta_banner", "value": "on", "description": "灰度横幅"}, adminToken); status != http.StatusOK {
		t.Fatalf("新增自定义配置应 200: %d", status)
	}
	// 更新同一键
	status, upd := request(http.MethodPut, "/api/v1/admin/configs",
		map[string]any{"key": "feature.beta_banner", "value": "off", "description": "灰度横幅"}, adminToken)
	if status != http.StatusOK || upd["data"].(map[string]any)["value"] != "off" {
		t.Fatalf("更新自定义配置失败: %d %v", status, upd)
	}

	// 4. 非法键被拒
	if status, _ := request(http.MethodPut, "/api/v1/admin/configs",
		map[string]any{"key": "bad key!", "value": "x"}, adminToken); status != http.StatusBadRequest {
		t.Fatalf("非法配置键应 400: %d", status)
	}

	// 5. 删除系统关键键被拒
	if status, _ := request(http.MethodDelete, "/api/v1/admin/configs/site_name", nil, adminToken); status != http.StatusBadRequest {
		t.Fatalf("删除系统关键键应 400: %d", status)
	}

	// 6. 删除自定义配置成功
	if status, _ := request(http.MethodDelete, "/api/v1/admin/configs/feature.beta_banner", nil, adminToken); status != http.StatusOK {
		t.Fatalf("删除自定义配置应 200: %d", status)
	}
}
