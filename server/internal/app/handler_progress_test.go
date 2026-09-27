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

// TestCurrentReadingStreak 覆盖连续阅读天数的边界：空、连读、今天/昨天起算、断档。
func TestCurrentReadingStreak(t *testing.T) {
	// 以“今天中午”为锚点，避免临近午夜时 AddDate 跨日导致抖动。
	noon := analyticsDayStart(time.Now()).Add(12 * time.Hour)
	day := func(n int) time.Time { return noon.AddDate(0, 0, -n) }

	cases := []struct {
		name  string
		times []time.Time
		want  int
	}{
		{"empty", nil, 0},
		{"today only", []time.Time{day(0)}, 1},
		{"today+yesterday+2days", []time.Time{day(0), day(1), day(2)}, 3},
		{"yesterday only (today missing)", []time.Time{day(1)}, 1},
		{"gap breaks streak", []time.Time{day(0), day(2)}, 1},
		{"stale last read (2 days ago)", []time.Time{day(2), day(3)}, 0},
		{"duplicates same day", []time.Time{day(0), day(0), day(1)}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := currentReadingStreak(tc.times); got != tc.want {
				t.Fatalf("currentReadingStreak(%v) = %d, want %d", tc.times, got, tc.want)
			}
		})
	}
}

// TestReadingMinuteGoalAndActivity 覆盖每日阅读时长累计 + 分钟制目标达标判定。
func TestReadingMinuteGoalAndActivity(t *testing.T) {
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
		"site":     map[string]any{"name": "时长目标测试站"},
		"admin":    map[string]any{"username": "author", "email": "author@test.local", "password": "secret123"},
	}, "")
	authorToken := installed["data"].(map[string]any)["token"].(string)

	_, createdBook := request(http.MethodPost, "/api/v1/books", map[string]any{
		"title": "Book", "status": "published", "is_public": true,
	}, authorToken)
	bookID := int(createdBook["data"].(map[string]any)["id"].(float64))
	_, createdDoc := request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), map[string]any{
		"title": "章节", "content": "正文", "status": "published",
	}, authorToken)
	doc := createdDoc["data"].(map[string]any)

	_, registered := request(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "reader", "email": "reader@test.local", "password": "secret123",
	}, "")
	readerToken := registered["data"].(map[string]any)["token"].(string)

	// 上报两次阅读时长共 20 分钟（1200 秒）。
	for _, delta := range []int{600, 600} {
		request(http.MethodPut, fmt.Sprintf("/api/v1/reading-progress/%d", bookID), map[string]any{
			"doc_id": int(doc["id"].(float64)), "doc_slug": doc["slug"].(string), "doc_title": doc["title"].(string),
			"read_seconds_delta": delta,
		}, readerToken)
	}

	// 默认章节制：读了 1 章，today_met 为 true；分钟累计到 20。
	_, act := request(http.MethodGet, "/api/v1/users/me/reading-activity", nil, readerToken)
	data := act["data"].(map[string]any)
	if data["today_minutes"] != float64(20) {
		t.Fatalf("今日阅读时长应为 20 分钟，实际 %v", data["today_minutes"])
	}
	if data["today_met"] != true {
		t.Fatalf("章节制默认目标(1 章)今日应达标")
	}
	if data["goal"].(map[string]any)["goal_type"] != "chapters" {
		t.Fatalf("默认目标类型应为 chapters")
	}

	// 切换为分钟制、每日 30 分钟：20 < 30 未达标。
	request(http.MethodPut, "/api/v1/users/me/reading-goal", map[string]any{"goal_type": "minutes", "daily_minutes": 30}, readerToken)
	_, act2 := request(http.MethodGet, "/api/v1/users/me/reading-activity", nil, readerToken)
	if act2["data"].(map[string]any)["today_met"] != false {
		t.Fatalf("分钟制目标 30 分钟时，20 分钟不应达标")
	}

	// 降到每日 15 分钟：20 ≥ 15 达标。
	request(http.MethodPut, "/api/v1/users/me/reading-goal", map[string]any{"goal_type": "minutes", "daily_minutes": 15}, readerToken)
	_, act3 := request(http.MethodGet, "/api/v1/users/me/reading-activity", nil, readerToken)
	d3 := act3["data"].(map[string]any)
	if d3["today_met"] != true {
		t.Fatalf("分钟制目标 15 分钟时，20 分钟应达标")
	}
	if d3["current_streak"] != float64(1) {
		t.Fatalf("今日达标，连续打卡应为 1，实际 %v", d3["current_streak"])
	}
}
