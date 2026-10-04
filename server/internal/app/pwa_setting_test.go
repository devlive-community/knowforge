package app

import (
	"net/http"
	"testing"

	"knowforge/server/internal/testdb"
)

// 安装为应用与离线阅读：默认开启（公开站点配置 pwa_enabled=true），管理员可关闭。
func TestPWASetting(t *testing.T) {
	_, h := newBackupTestApp(t, t.TempDir())
	_, installed := h.do("", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"PWA"},"admin":{"username":"pwa-admin","email":"pwa-admin@test.local","password":"secret123"}}`)
	adminToken := backupData(installed)["token"].(string)
	if _, p := h.do("", http.MethodGet, "/api/v1/site", ""); backupData(p)["pwa_enabled"] != true {
		t.Fatalf("默认应开启: %v", backupData(p)["pwa_enabled"])
	}
	if status, _ := h.do(adminToken, http.MethodPut, "/api/v1/site", `{"pwa_enabled":false}`); status != http.StatusOK {
		t.Fatalf("保存失败: %d", status)
	}
	if _, p := h.do("", http.MethodGet, "/api/v1/site", ""); backupData(p)["pwa_enabled"] != false {
		t.Fatalf("关闭后应为 false: %v", backupData(p)["pwa_enabled"])
	}
}
