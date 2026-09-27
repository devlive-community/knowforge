package storage

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalDelete(t *testing.T) {
	dir := t.TempDir()
	up := &LocalUploader{dataDir: dir}
	if _, err := up.Upload("a.png", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := up.Delete("a.png"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "uploads", "a.png")); !os.IsNotExist(err) {
		t.Fatalf("文件应已删除: %v", err)
	}
	if err := up.Delete("a.png"); err != nil {
		t.Fatalf("文件不存在时视为已删除: %v", err)
	}
	for _, bad := range []string{"../x", "a/b", "..", ""} {
		if err := up.Delete(bad); err == nil {
			t.Fatalf("非法文件名应被拒绝: %q", bad)
		}
	}
}

func TestS3Delete(t *testing.T) {
	var method, path, auth string
	status := http.StatusNoContent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		w.WriteHeader(status)
	}))
	defer srv.Close()
	up := FromConfig(Config{Driver: "s3", S3Endpoint: srv.URL, S3Bucket: "media", S3AccessKey: "AK", S3SecretKey: "SK", S3PathStyle: true, S3Prefix: "kf"}, t.TempDir())
	if err := up.(Deleter).Delete("a.png"); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodDelete || path != "/media/kf/a.png" || !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AK/") {
		t.Fatalf("删除请求错误: %s %s %s", method, path, auth)
	}
	status = http.StatusNotFound
	if err := up.(Deleter).Delete("a.png"); err != nil {
		t.Fatalf("对象不存在视为已删除: %v", err)
	}
	status = http.StatusForbidden
	if err := up.(Deleter).Delete("a.png"); err == nil {
		t.Fatal("拒绝访问应返回错误")
	}
}

func TestQiniuDelete(t *testing.T) {
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	old := qiniuRSHost
	qiniuRSHost = srv.URL
	defer func() { qiniuRSHost = old }()
	up := FromConfig(Config{Driver: "qiniu", QiniuAccessKey: "AK", QiniuSecretKey: "SK", QiniuBucket: "bk", QiniuDomain: "https://cdn.example.com"}, t.TempDir())
	if err := up.(Deleter).Delete("a.png"); err != nil {
		t.Fatal(err)
	}
	wantPath := "/delete/" + base64.URLEncoding.EncodeToString([]byte("bk:a.png"))
	mac := hmac.New(sha1.New, []byte("SK"))
	mac.Write([]byte(wantPath + "\n"))
	if path != wantPath || auth != "QBox AK:"+urlsafeBase64(mac.Sum(nil)) {
		t.Fatalf("七牛删除请求错误: %s %s", path, auth)
	}
}

func TestDriverName(t *testing.T) {
	if DriverName(&LocalUploader{}) != "local" || DriverName(&S3Uploader{}) != "s3" || DriverName(&QiniuUploader{}) != "qiniu" {
		t.Fatal("驱动名称错误")
	}
}
