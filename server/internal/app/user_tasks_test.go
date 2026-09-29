package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/jobqueue"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// 我的任务：合并核心任务与插件任务（按创建时间倒序、按分组筛选、只含本人的），任务变化经通知事件流推送。
func TestMyTasks(t *testing.T) {
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
	do := func(token, path string) (int, map[string]any) {
		req, _ := http.NewRequest(http.MethodGet, server.URL+path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		p := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&p)
		return resp.StatusCode, p
	}
	items := func(token, tab string) []map[string]any {
		status, p := do(token, "/api/v1/users/me/tasks?tab="+tab)
		if status != http.StatusOK {
			t.Fatalf("查询任务失败 %s: %d %v", tab, status, p)
		}
		out := []map[string]any{}
		for _, it := range p["data"].(map[string]any)["items"].([]any) {
			out = append(out, it.(map[string]any))
		}
		return out
	}
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/setup/install", bytes.NewReader([]byte(`{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"任务"},"admin":{"username":"tk-admin","email":"tk-admin@test.local","password":"secret123"}}`)))
	req.Header.Set("Content-Type", "application/json")
	if resp, err := http.DefaultClient.Do(req); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
	}
	u := &models.User{Username: "tk-user", Email: "tk-user@test.local", Role: "user", IsActive: true, EmailVerified: true}
	other := &models.User{Username: "tk-other", Email: "tk-other@test.local", Role: "user", IsActive: true, EmailVerified: true}
	a.DB.Create(u)
	a.DB.Create(other)
	token, _ := auth.GenerateToken(a.Config.Secret, u.ID, u.Username, u.Role)
	otherToken, _ := auth.GenerateToken(a.Config.Secret, other.ID, other.Username, other.Role)
	book := models.Book{Title: "任务之书", Slug: "task-book", UserID: u.ID, Status: "draft"}
	a.DB.Create(&book)
	if a.jobQueue() == nil {
		if err := a.configureJobQueue(); err != nil {
			t.Fatal(err)
		}
	}

	// 未知分组
	if status, _ := do(token, "/api/v1/users/me/tasks?tab=bad"); status != http.StatusBadRequest {
		t.Fatalf("未知分组应 400: %d", status)
	}

	// 插件任务：书籍多语言的翻译任务（进行中，含进度），以及较早的已完成任务
	base := time.Now().Add(-time.Hour)
	a.DB.Table("book_ai_translate_jobs").Create(map[string]any{"user_id": u.ID, "source_book_id": book.ID, "target_lang": "en", "target_label": "English",
		"status": "running", "total": 10, "done": 4, "created_at": base, "updated_at": base})
	a.DB.Table("book_ai_translate_jobs").Create(map[string]any{"user_id": u.ID, "source_book_id": book.ID, "target_lang": "ja", "target_label": "日本語",
		"status": "done", "total": 3, "done": 3, "created_at": base, "updated_at": base})

	// 核心任务入队即推送给发起人
	sub := a.Notifications.subscribe(u.ID)
	defer a.Notifications.unsubscribe(u.ID, sub)
	job, err := a.jobQueue().EnqueueOwned(context.Background(), u.ID, imageLocalizeJobType, imageLocalizeJob{UserID: u.ID, BookID: book.ID}, 1)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-sub:
		var pushed struct {
			Task map[string]any `json:"task"`
		}
		if json.Unmarshal([]byte(msg), &pushed) != nil || pushed.Task["kind"] != userTaskImageLocalize || pushed.Task["status"] != "queued" || pushed.Task["title"] != "任务之书" {
			t.Fatalf("入队推送异常: %s", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("入队后应推送任务")
	}

	active := items(token, "active")
	if len(active) != 2 || active[0]["kind"] != userTaskImageLocalize || active[1]["kind"] != "translate" {
		t.Fatalf("进行中应含本地化与翻译任务（新→旧）: %v", active)
	}
	if tr := active[1]; tr["done"].(float64) != 4 || tr["total"].(float64) != 10 || tr["title"] != "任务之书 → English" ||
		!strings.HasPrefix(tr["link"].(string), "/book/settings/task-book/ai-translate?job=") {
		t.Fatalf("翻译任务展示异常: %v", tr)
	}
	if active[0]["link"] != "/book/settings/task-book/cleanup" {
		t.Fatalf("本地化任务链接异常: %v", active[0])
	}
	if done := items(token, "done"); len(done) != 1 || done[0]["title"] != "任务之书 → 日本語" {
		t.Fatalf("已完成分组异常: %v", done)
	}
	if list := items(otherToken, "active"); len(list) != 0 {
		t.Fatalf("不应看到他人的任务: %v", list)
	}

	// 核心任务失败后进入失败分组
	a.DB.Model(&models.BackgroundJob{}).Where("id = ?", job.ID).Updates(map[string]any{"status": jobqueue.StatusFailed, "last_error": "网络错误"})
	failed := items(token, "failed")
	if len(failed) != 1 || failed[0]["id"] != fmt.Sprint(job.ID) || failed[0]["error"] != "网络错误" {
		t.Fatalf("失败分组异常: %v", failed)
	}
}
