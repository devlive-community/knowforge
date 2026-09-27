package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// 加固测试：覆盖此前测试未守护的边界。

// M11：登录用户搜索应能看到自己的私有书籍，他人不可见
func TestSearchLoggedInVisibility(t *testing.T) {
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
		"site":     map[string]any{"name": "可见性测试站"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	adminToken := install["data"].(map[string]any)["token"].(string)
	_, reg := request(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "alice", "email": "alice@test.local", "password": "secret123",
	}, "")
	aliceToken := reg["data"].(map[string]any)["token"].(string)
	_ = adminToken

	request(http.MethodPost, "/api/v1/books", map[string]any{
		"title": "私密可见性之书", "status": "published", "is_public": false,
	}, aliceToken)

	searchPath := "/api/v1/search?q=" + url.QueryEscape("可见性")
	status, anon := request(http.MethodGet, searchPath, nil, "")
	if status != 200 {
		t.Fatalf("匿名搜索失败: %d", status)
	}
	if len(anon["data"].(map[string]any)["books"].([]any)) != 0 {
		t.Fatal("匿名搜索不应命中私有书籍")
	}
	status, own := request(http.MethodGet, searchPath, nil, aliceToken)
	if status != 200 {
		t.Fatalf("登录搜索失败: %d", status)
	}
	if len(own["data"].(map[string]any)["books"].([]any)) != 1 {
		t.Fatal("登录用户应能搜到自己的私有书籍")
	}
}

// M16：导入 zip 的路径穿越与文件数上限必须拒绝
func TestImportSecurity(t *testing.T) {
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
		"site":     map[string]any{"name": "导入安全测试站"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	adminToken := install["data"].(map[string]any)["token"].(string)

	importZip := func(raw []byte) (int, map[string]any) {
		t.Helper()
		body := &bytes.Buffer{}
		mw := multipart.NewWriter(body)
		fw, _ := mw.CreateFormFile("file", "evil.zip")
		_, _ = fw.Write(raw)
		_ = mw.Close()
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/import", body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+adminToken)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("导入请求失败: %v", err)
		}
		defer resp.Body.Close()
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		if resp.StatusCode == http.StatusAccepted {
			if ran, runErr := a.Jobs.RunOnce(context.Background()); !ran {
				t.Fatal("ZIP import task was not claimed")
			} else if runErr != nil {
				return http.StatusBadRequest, map[string]any{"message": runErr.Error()}
			}
			return http.StatusOK, payload
		}
		return resp.StatusCode, payload
	}
	buildZip := func(files map[string][]byte) []byte {
		buf := &bytes.Buffer{}
		zw := zip.NewWriter(buf)
		for name, data := range files {
			f, _ := zw.Create(name)
			f.Write(data)
		}
		zw.Close()
		return buf.Bytes()
	}
	bookMD := "---\ntitle: \"X\"\n---\n"

	// 路径穿越：zip 内含 ../ 路径
	status, _ := importZip(buildZip(map[string][]byte{
		"book.md":       []byte(bookMD),
		"../payload.sh": []byte("evil"),
	}))
	if status != 400 {
		t.Fatalf("含 ../ 路径的 zip 应 400: %d", status)
	}
	// 绝对路径同理
	status, _ = importZip(buildZip(map[string][]byte{
		"book.md":      []byte(bookMD),
		"/etc/payload": []byte("evil"),
	}))
	if status != 400 {
		t.Fatalf("含绝对路径的 zip 应 400: %d", status)
	}
	// 文件数超限（501 个文件）
	many := map[string][]byte{"book.md": []byte(bookMD)}
	for i := 0; i < 501; i++ {
		many[fmt.Sprintf("chapters/%03d.md", i)] = []byte("x")
	}
	status, _ = importZip(buildZip(many))
	if status != 400 {
		t.Fatalf("超文件数上限应 400: %d", status)
	}
	// 正常导入仍可用（回归）
	status, ok := importZip(buildZip(map[string][]byte{
		"book.md":              []byte(bookMD),
		"chapters/01-hello.md": []byte("---\ntitle: \"你好\"\nslug: \"hello\"\n---\n内容"),
	}))
	if status != 200 {
		t.Fatalf("正常 zip 应导入成功: %d %v", status, ok)
	}
}

// M14×M16：editor 协作者可导出，viewer 不可
func TestExportByCollaborator(t *testing.T) {
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
		// 导出端点返回 zip，JSON 解码失败仅得到 nil payload，状态码断言不受影响
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return resp.StatusCode, payload
	}

	request(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "协作者导出测试站"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	register := func(username string) string {
		_, reg := request(http.MethodPost, "/api/v1/auth/register", map[string]any{
			"username": username, "email": username + "@test.local", "password": "secret123",
		}, "")
		return reg["data"].(map[string]any)["token"].(string)
	}
	aliceToken := register("alice")
	bobToken := register("bob")
	carolToken := register("carol")

	status, book := request(http.MethodPost, "/api/v1/books", map[string]any{
		"title": "导出之书", "status": "published", "is_public": true,
	}, aliceToken)
	bookID := int(book["data"].(map[string]any)["id"].(float64))
	_, bobInvite := request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/collaborators", bookID), map[string]any{
		"username": "bob", "role": "editor",
	}, aliceToken)
	_, carolInvite := request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/collaborators", bookID), map[string]any{
		"username": "carol", "role": "viewer",
	}, aliceToken)
	bobInviteID := int(bobInvite["data"].(map[string]any)["id"].(float64))
	carolInviteID := int(carolInvite["data"].(map[string]any)["id"].(float64))
	request(http.MethodPost, fmt.Sprintf("/api/v1/collaboration/invitations/%d/accept", bobInviteID), nil, bobToken)
	request(http.MethodPost, fmt.Sprintf("/api/v1/collaboration/invitations/%d/accept", carolInviteID), nil, carolToken)

	// viewer 导出 → 403；editor 导出 → 200 zip
	status, _ = request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d/export", bookID), nil, carolToken)
	if status != 403 {
		t.Fatalf("viewer 导出应 403: %d", status)
	}
	status, _ = request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d/export", bookID), nil, bobToken)
	if status != 200 {
		t.Fatalf("editor 导出应 200: %d", status)
	}
}

// M15：过期的找回令牌必须拒绝
func TestPasswordResetExpiredToken(t *testing.T) {
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
	request(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "过期令牌测试站"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")

	request(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "alice", "email": "alice@test.local", "password": "secret123",
	}, "")
	var alice models.User
	a.DB.Where("username = ?", "alice").First(&alice)

	// 直接向库里插一条已过期的令牌
	expired := "deadbeef" + fmt.Sprint(time.Now().UnixNano())
	sum := sha256.Sum256([]byte(expired))
	a.DB.Create(&models.PasswordResetToken{
		UserID:    alice.ID,
		TokenHash: hex.EncodeToString(sum[:]),
		ExpiresAt: time.Now().Add(-time.Hour),
	})

	status, _ := request(http.MethodPost, "/api/v1/auth/password/reset", map[string]any{
		"token": expired, "password": "newpass123",
	}, "")
	if status != 400 {
		t.Fatalf("过期令牌应 400: %d", status)
	}
	// 密码未被更改
	status, _ = request(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": "alice", "password": "secret123",
	}, "")
	if status != 200 {
		t.Fatalf("过期令牌不应改变原密码登录: %d", status)
	}
}
