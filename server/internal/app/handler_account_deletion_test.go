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

func TestAccountDeletionFlow(t *testing.T) {
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
	req := func(method, path string, body any, token string) (int, map[string]any) {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		r, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		p := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&p)
		return resp.StatusCode, p
	}

	req(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "注销测试"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")

	register := func(name string) (string, uint) {
		_, r := req(http.MethodPost, "/api/v1/auth/register", map[string]any{
			"username": name, "email": name + "@test.local", "password": "secret123",
		}, "")
		d := r["data"].(map[string]any)
		return d["token"].(string), uint(d["user"].(map[string]any)["id"].(float64))
	}
	mkBook := func(token string) {
		_, b := req(http.MethodPost, "/api/v1/books", map[string]any{"title": "书", "status": "published"}, token)
		id := int(b["data"].(map[string]any)["id"].(float64))
		req(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", id), map[string]any{"title": "章", "content": "正文"}, token)
	}

	// 1. 冷静期 7 天：申请 → 进入冷静期；错误密码被拒
	token, uid := register("victim")
	mkBook(token)
	if s, _ := req(http.MethodPost, "/api/v1/auth/account/deletion", map[string]any{"password": "wrong"}, token); s != http.StatusBadRequest {
		t.Fatalf("错误密码应 400: %d", s)
	}
	s, resp := req(http.MethodPost, "/api/v1/auth/account/deletion", map[string]any{"password": "secret123"}, token)
	if s != http.StatusOK || resp["data"].(map[string]any)["requested"] != true {
		t.Fatalf("申请注销应成功进入冷静期: %d %v", s, resp)
	}
	if resp["data"].(map[string]any)["scheduled_delete_at"] == nil {
		t.Fatalf("应返回预定删除时间")
	}
	// 撤销
	req(http.MethodDelete, "/api/v1/auth/account/deletion", nil, token)
	_, st := req(http.MethodGet, "/api/v1/auth/account/deletion", nil, token)
	if st["data"].(map[string]any)["requested"] != false {
		t.Fatalf("撤销后应无注销申请")
	}

	// 2. 冷静期 0：确认即删除，用户与其书籍一并清除
	_ = a.setSetting(cfgAcctDelCooldown, "0", "")
	s, _ = req(http.MethodPost, "/api/v1/auth/account/deletion", map[string]any{"password": "secret123"}, token)
	if s != http.StatusOK {
		t.Fatalf("冷静期 0 时应立即删除: %d", s)
	}
	var uc, bc int64
	a.DB.Model(&models.User{}).Where("id = ?", uid).Count(&uc)
	a.DB.Model(&models.Book{}).Where("user_id = ?", uid).Count(&bc)
	if uc != 0 || bc != 0 {
		t.Fatalf("即时删除后用户与书籍应清空 uc=%d bc=%d", uc, bc)
	}

	// 3. 冷静期到期自动清理
	_ = a.setSetting(cfgAcctDelCooldown, "7", "")
	token2, uid2 := register("victim2")
	mkBook(token2)
	req(http.MethodPost, "/api/v1/auth/account/deletion", map[string]any{"password": "secret123"}, token2)
	// 把申请时间回拨到 8 天前
	a.DB.Model(&models.User{}).Where("id = ?", uid2).Update("deletion_requested_at", time.Now().AddDate(0, 0, -8))
	if err := a.purgeScheduledAccountDeletions(time.Now()); err != nil {
		t.Fatalf("到期清理失败: %v", err)
	}
	a.DB.Model(&models.User{}).Where("id = ?", uid2).Count(&uc)
	if uc != 0 {
		t.Fatalf("到期后应自动删除")
	}

	// 4. 最后一位管理员不可注销
	_, adminLogin := req(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": "admin", "password": "secret123"}, "")
	adminToken := adminLogin["data"].(map[string]any)["token"].(string)
	if s, _ := req(http.MethodPost, "/api/v1/auth/account/deletion", map[string]any{"password": "secret123"}, adminToken); s != http.StatusBadRequest {
		t.Fatalf("最后一位管理员注销应 400: %d", s)
	}
}
