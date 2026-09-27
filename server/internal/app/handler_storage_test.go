package app

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"knowforge/server/internal/config"
	"knowforge/server/internal/testdb"
)

// 存储驱动集成测试：本地上传回归 / 管理端配置读写与权限 / 七牛驱动全流程（模拟上传端点）
func TestStorageDrivers(t *testing.T) {
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

	// 模拟七牛上传端点：校验 multipart 并返回 key
	var gotToken, gotKey string
	fakeQiniu := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotToken = r.FormValue("token")
		gotKey = r.FormValue("key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":"` + gotKey + `","hash":"x"}`))
	}))
	defer fakeQiniu.Close()

	_, install := request(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "存储测试站"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	adminToken := install["data"].(map[string]any)["token"].(string)

	upload := func(token string) (int, map[string]any) {
		t.Helper()
		body := &bytes.Buffer{}
		mw := multipart.NewWriter(body)
		fw, _ := mw.CreateFormFile("file", "photo.png")
		_, _ = fw.Write(bytes.Repeat([]byte{0x89, 'P', 'N', 'G'}, 32))
		_ = mw.Close()
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/upload", body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("上传请求失败: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var payload map[string]any
		_ = json.Unmarshal(raw, &payload)
		return resp.StatusCode, payload
	}

	// 1. 本地驱动（默认）：文件落盘，返回 /uploads 相对地址
	status, up := upload(adminToken)
	if status != 200 {
		t.Fatalf("本地上传失败: %d %v", status, up)
	}
	url := up["data"].(map[string]any)["url"].(string)
	if !strings.HasPrefix(url, "/uploads/") {
		t.Fatalf("本地上传应返回 /uploads 地址: %q", url)
	}
	if _, err := os.Stat(filepath.Join(config.DataDir(), "uploads", strings.TrimPrefix(url, "/uploads/"))); err != nil {
		t.Fatalf("文件应落盘: %v", err)
	}

	// 2. 存储配置：未登录 401；非法驱动 400
	status, _ = request(http.MethodPut, "/api/v1/storage", map[string]any{"driver": "qiniu"}, "")
	if status != 401 {
		t.Fatalf("未登录保存存储配置应 401: %d", status)
	}
	status, _ = request(http.MethodPut, "/api/v1/storage", map[string]any{"driver": "oss"}, adminToken)
	if status != 400 {
		t.Fatalf("非法驱动应 400: %d", status)
	}

	// 3. 切到七牛驱动（上传地址指向模拟端点），域名非法时 400
	status, _ = request(http.MethodPut, "/api/v1/storage", map[string]any{
		"driver": "qiniu", "qiniu_access_key": "ak-1", "qiniu_secret_key": "sk-1",
		"qiniu_bucket": "knowforge", "qiniu_domain": "cdn.example.com",
	}, adminToken)
	if status != 400 {
		t.Fatalf("域名缺少协议应 400: %d", status)
	}
	status, _ = request(http.MethodPut, "/api/v1/storage", map[string]any{
		"driver": "qiniu", "qiniu_access_key": "ak-1", "qiniu_secret_key": "sk-1",
		"qiniu_bucket": "knowforge", "qiniu_domain": "https://cdn.example.com",
		"qiniu_upload_host": fakeQiniu.URL,
	}, adminToken)
	if status != 200 {
		t.Fatalf("保存七牛配置失败: %d", status)
	}
	status, got := request(http.MethodGet, "/api/v1/storage", nil, adminToken)
	data := got["data"].(map[string]any)
	if status != 200 || data["driver"] != "qiniu" || data["qiniu_bucket"] != "knowforge" {
		t.Fatalf("存储配置读取异常: %d %v", status, data)
	}

	// 4. 七牛上传全流程：返回 CDN 绝对地址，模拟端点收到合法 token/key
	status, up = upload(adminToken)
	if status != 200 {
		t.Fatalf("七牛上传失败: %d %v", status, up)
	}
	url = up["data"].(map[string]any)["url"].(string)
	if !strings.HasPrefix(url, "https://cdn.example.com/") {
		t.Fatalf("七牛驱动应返回 CDN 绝对地址: %q", url)
	}
	if gotKey != strings.TrimPrefix(url, "https://cdn.example.com/") {
		t.Fatalf("上传 key 与返回 URL 不一致: key=%q url=%q", gotKey, url)
	}
	parts := strings.Split(gotToken, ":")
	if len(parts) != 3 || parts[0] != "ak-1" {
		t.Fatalf("上传 token 格式异常: %q", gotToken)
	}

	// 5. S3 兼容存储（路径风格，模拟端点校验 SigV4 签名头）；Secret Key 只写不读
	var s3Path, s3Auth string
	fakeS3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s3Path, s3Auth = r.URL.Path, r.Header.Get("Authorization")
		if r.Method != http.MethodPut || !strings.HasPrefix(s3Auth, "AWS4-HMAC-SHA256 Credential=s3-ak/") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer fakeS3.Close()
	status, _ = request(http.MethodPut, "/api/v1/storage", map[string]any{
		"driver": "s3", "s3_endpoint": fakeS3.URL, "s3_region": "us-east-1", "s3_bucket": "kf", "s3_access_key": "s3-ak",
		"s3_secret_key": "s3-sk", "s3_path_style": true, "s3_public_url": "https://img.example.com", "s3_prefix": "uploads",
	}, adminToken)
	if status != 200 {
		t.Fatalf("保存 S3 配置失败: %d", status)
	}
	_, got = request(http.MethodGet, "/api/v1/storage", nil, adminToken)
	data = got["data"].(map[string]any)
	if _, leaked := data["s3_secret_key"]; leaked || data["s3_secret_key_set"] != true || data["s3_path_style"] != true {
		t.Fatalf("S3 Secret Key 不应回显: %v", data)
	}
	// 只改其它字段、Secret Key 传空串时保持原值
	request(http.MethodPut, "/api/v1/storage", map[string]any{"s3_secret_key": "", "s3_region": "us-east-1"}, adminToken)
	status, up = upload(adminToken)
	if status != 200 {
		t.Fatalf("S3 上传失败: %d %v", status, up)
	}
	url = up["data"].(map[string]any)["url"].(string)
	if !strings.HasPrefix(url, "https://img.example.com/uploads/") || s3Path != "/kf/uploads/"+strings.TrimPrefix(url, "https://img.example.com/uploads/") {
		t.Fatalf("S3 上传地址/对象键错误: url=%q path=%q", url, s3Path)
	}

	// 6. 切回 local 驱动后恢复相对地址
	request(http.MethodPut, "/api/v1/storage", map[string]any{"driver": "local"}, adminToken)
	status, up = upload(adminToken)
	if status != 200 || !strings.HasPrefix(up["data"].(map[string]any)["url"].(string), "/uploads/") {
		t.Fatalf("切回本地驱动应恢复相对地址: %d %v", status, up)
	}
}
