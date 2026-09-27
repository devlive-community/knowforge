package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"knowforge/server/internal/cluster"
	"knowforge/server/internal/config"
)

// 多实例状态：列出在线实例；另一个实例的标记文件不可见时提示数据目录未共享，并提示版本不一致与在线升级只升级当前实例。
func TestAdminClusterStatus(t *testing.T) {
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
	resp, err := http.Post(server.URL+"/api/v1/setup/install", "application/json", strings.NewReader(
		`{"database":{"type":"sqlite"},"site":{"name":"集群测试"},"admin":{"username":"admin","email":"admin@test.local","password":"secret123"}}`))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("安装失败: %v %v", err, resp)
	}
	var installed struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&installed)
	resp.Body.Close()
	status := func() (int, []string, int) {
		req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/system/cluster", nil)
		req.Header.Set("Authorization", "Bearer "+installed.Data.Token)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var p struct {
			Data struct {
				Instances []map[string]any `json:"instances"`
				Warnings  []string         `json:"warnings"`
			} `json:"data"`
		}
		_ = json.NewDecoder(r.Body).Decode(&p)
		return r.StatusCode, p.Data.Warnings, len(p.Data.Instances)
	}

	if err := cluster.Start(context.Background(), a.DB, Version); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cluster.Stop)
	writeClusterMarker()
	if code, warnings, n := status(); code != http.StatusOK || n != 1 || len(warnings) != 0 {
		t.Fatalf("单实例不应有警告: %d %v %d", code, warnings, n)
	}

	a.DB.Create(&cluster.Instance{ID: "peer", Version: "0.0.1", StartedAt: time.Now(), SeenAt: time.Now()})
	_, warnings, n := status()
	got := strings.Join(warnings, ",")
	if n != 2 || !strings.Contains(got, "data_dir") || !strings.Contains(got, "version") || !strings.Contains(got, "upgrade") {
		t.Fatalf("应提示数据目录未共享、版本不一致与在线升级: %v", warnings)
	}
	// 能看到对方的标记文件后不再提示数据目录未共享
	_ = os.WriteFile(filepath.Join(clusterMarkerDir(), "peer"), []byte("x"), 0o644)
	if _, warnings, _ := status(); strings.Contains(strings.Join(warnings, ","), "data_dir") {
		t.Fatalf("看到标记文件后不应提示数据目录未共享: %v", warnings)
	}
	removeClusterMarker()
	if _, err := os.Stat(filepath.Join(clusterMarkerDir(), cluster.Self())); !os.IsNotExist(err) {
		t.Fatal("停止时应移除本实例的标记文件")
	}
}
