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

// TestBookTranslationsGrouping 覆盖翻译组：同 trans_group 的可见书籍聚合、私有书对匿名不可见、少于两本不成组。
func TestBookTranslationsGrouping(t *testing.T) {
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

	request := func(method, path string, body any, token string) (int, map[string]any) {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
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
		"site":     map[string]any{"name": "多语言测试"},
		"admin":    map[string]any{"username": "author", "email": "author@test.local", "password": "secret123"},
	}, "")
	token := installed["data"].(map[string]any)["token"].(string)

	mk := func(title, lang, group string, public bool) int {
		_, created := request(http.MethodPost, "/api/v1/books", map[string]any{
			"title": title, "status": "published", "is_public": public, "language": lang, "trans_group": group,
		}, token)
		return int(created["data"].(map[string]any)["id"].(float64))
	}
	zh := mk("中文书", "中文", "grp1", true)
	mk("English Book", "English", "grp1", true)
	mk("私有译本", "日本語", "grp1", false)
	solo := mk("孤立书", "中文", "grp2", true)

	// 匿名读取中文书的翻译组：公开的两本，私有排除
	status, resp := request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d/translations", zh), nil, "")
	if status != http.StatusOK {
		t.Fatalf("读取翻译组失败: %d", status)
	}
	items := resp["data"].(map[string]any)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("匿名应见 2 本公开译本，实际 %d", len(items))
	}
	// 作者本人可见全部 3 本
	status, respAuthor := request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d/translations", zh), nil, token)
	if status != http.StatusOK || len(respAuthor["data"].(map[string]any)["items"].([]any)) != 3 {
		t.Fatalf("作者应见 3 本（含私有）")
	}
	// 孤立书不成组
	_, respSolo := request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d/translations", solo), nil, token)
	if len(respSolo["data"].(map[string]any)["items"].([]any)) != 0 {
		t.Fatalf("单本不应构成翻译组")
	}
}

// TestBookVersionsGroupedListing 覆盖「版本聚合」：列表服务端按版本组聚合（优先最新版）、分页总数准确、
// 回填 version_count / latest_version；版本弹框接口按版本号排序分页。
func TestBookVersionsGroupedListing(t *testing.T) {
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
	request := func(method, path string, body any, token string) (int, map[string]any) {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
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
		"site":     map[string]any{"name": "版本测试"},
		"admin":    map[string]any{"username": "author", "email": "author@test.local", "password": "secret123"},
	}, "")
	token := installed["data"].(map[string]any)["token"].(string)
	request(http.MethodPost, "/api/v1/admin/plugins/book-versions/install", nil, token)

	mk := func(title, version, group string, latest bool) int {
		_, created := request(http.MethodPost, "/api/v1/books", map[string]any{
			"title": title, "status": "published", "is_public": true, "version": version, "version_group": group, "version_is_latest": latest,
		}, token)
		return int(created["data"].(map[string]any)["id"].(float64))
	}
	mk("手册 v1.9", "v1.9", "manual", false)
	latestID := mk("手册 v1.10", "v1.10", "manual", true)
	mk("手册 v1.2", "v1.2", "manual", false)
	mk("独立书", "", "", false)

	page := func(path string) (items []any, total int) {
		status, resp := request(http.MethodGet, path, nil, "")
		if status != http.StatusOK {
			t.Fatalf("GET %s 失败: %d %v", path, status, resp)
		}
		data := resp["data"].(map[string]any)
		return data["items"].([]any), int(data["total"].(float64))
	}
	if _, total := page("/api/v1/books"); total != 4 {
		t.Fatalf("未聚合应返回 4 本，实际 %d", total)
	}
	items, total := page("/api/v1/books?group_versions=true")
	if total != 2 || len(items) != 2 {
		t.Fatalf("聚合后应返回 2 本，实际 total=%d len=%d", total, len(items))
	}
	for _, it := range items {
		b := it.(map[string]any)
		if b["version_group"] == "manual" {
			if int(b["id"].(float64)) != latestID || int(b["version_count"].(float64)) != 3 || b["latest_version"] != "v1.10" {
				t.Fatalf("版本组代表应为最新版并带 version_count=3 / latest_version=v1.10: %v", b)
			}
		}
	}
	if items, total := page("/api/v1/users/author/books?group_versions=true"); total != 2 || len(items) != 2 {
		t.Fatalf("用户主页聚合后应返回 2 本，实际 total=%d", total)
	}

	// 版本弹框：默认按版本号倒序（v1.10 > v1.9 > v1.2），分页
	items, total = page(fmt.Sprintf("/api/v1/books/%d/versions/books?page=1&page_size=2", latestID))
	if total != 3 || len(items) != 2 || items[0].(map[string]any)["version"] != "v1.10" || items[1].(map[string]any)["version"] != "v1.9" {
		t.Fatalf("版本列表排序/分页错误: total=%d items=%v", total, items)
	}
	items, _ = page(fmt.Sprintf("/api/v1/books/%d/versions/books?page=2&page_size=2", latestID))
	if len(items) != 1 || items[0].(map[string]any)["version"] != "v1.2" {
		t.Fatalf("版本列表第 2 页错误: %v", items)
	}

	// 译本继承原书的版本组：同一版本的多个语言只算一个版本，版本切换优先当前语言（未启用「书籍多语言」时不按语言筛选）
	request(http.MethodPost, "/api/v1/admin/plugins/book-translations/uninstall", nil, token)
	_, created := request(http.MethodPost, "/api/v1/books", map[string]any{
		"title": "Manual v1.10", "status": "published", "is_public": true, "version": "v1.10", "version_group": "manual",
		"version_is_latest": true, "language": "English",
	}, token)
	enID := int(created["data"].(map[string]any)["id"].(float64))
	if items, _ := page("/api/v1/books?group_versions=true"); len(items) == 2 {
		for _, it := range items {
			if b := it.(map[string]any); b["version_group"] == "manual" && int(b["version_count"].(float64)) != 3 {
				t.Fatalf("译本不应增加版本数: %v", b)
			}
		}
	}
	for _, id := range []int{latestID, enID} {
		status, resp := request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d/versions", id), nil, "")
		if status != http.StatusOK {
			t.Fatalf("读取版本失败: %d", status)
		}
		variants := resp["data"].(map[string]any)["items"].([]any)
		current := 0
		for _, v := range variants {
			if v.(map[string]any)["version"] == "v1.10" {
				if !v.(map[string]any)["current"].(bool) {
					t.Fatalf("v1.10 应为当前书籍（%d）: %v", id, v)
				}
				current++
			}
		}
		if len(variants) != 3 || current != 1 {
			t.Fatalf("每个版本号应只出现一次（%d）: %v", id, variants)
		}
	}
	if _, total := page(fmt.Sprintf("/api/v1/books/%d/versions/books", enID)); total != 3 {
		t.Fatalf("版本弹框应按版本号去重，实际 %d", total)
	}

	// 启用「书籍多语言」后只列出当前语言已有的版本：英文只做了 v1.10，不显示只有原文的版本（只剩一本时不显示切换）
	request(http.MethodPost, "/api/v1/admin/plugins/book-translations/install", nil, token)
	versionsOf := func(id int) []any {
		_, resp := request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d/versions", id), nil, "")
		return resp["data"].(map[string]any)["items"].([]any)
	}
	if got := versionsOf(enID); len(got) != 0 {
		t.Fatalf("英文译本只有一个版本，不应列出其他语言的版本: %v", got)
	}
	if got := versionsOf(latestID); len(got) != 3 {
		t.Fatalf("原文应列出全部 3 个原文版本: %v", got)
	}
	if _, total := page(fmt.Sprintf("/api/v1/books/%d/versions/books", enID)); total != 1 {
		t.Fatalf("版本弹框也只列出当前语言的版本，实际 %d", total)
	}
}
