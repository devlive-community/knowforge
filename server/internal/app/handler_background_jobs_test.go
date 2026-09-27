package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"knowforge/server/internal/config"
	"knowforge/server/internal/jobqueue"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

func TestCleanupExpiredImportSources(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("KNOWFORGE_DATA", dataDir)
	dir := filepath.Join(dataDir, "import-jobs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	stale := filepath.Join(dir, "zip-stale.zip")
	fresh := filepath.Join(dir, "pdf-fresh.pdf")
	unrelated := filepath.Join(dir, "keep.txt")
	for _, path := range []string{stale, fresh, unrelated} {
		if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := now.Add(-31 * 24 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if err := cleanupExpiredImportSources(now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("expired import source was not removed: %v", err)
	}
	for _, path := range []string{fresh, unrelated} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("active or unrelated file was removed: %s: %v", path, err)
		}
	}
}

func TestMaintenanceCleanupRunsFromPersistentQueueAndIsIdempotent(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("KNOWFORGE_DATA", dataDir)
	a, owner, db := newContentImportTestApp(t)
	a.Config = &config.Config{Secret: "maintenance-background-task-secret"}
	if err := a.configureJobQueue(); err != nil {
		t.Fatal(err)
	}

	book := models.Book{Title: "过期回收站书籍", Slug: "expired-trash-book", UserID: owner.ID, Status: "draft"}
	if err := db.Create(&book).Error; err != nil {
		t.Fatal(err)
	}
	doc := models.Document{BookID: book.ID, UserID: owner.ID, Title: "待清理章节", Slug: "expired-doc", Status: "draft"}
	if err := db.Create(&doc).Error; err != nil {
		t.Fatal(err)
	}
	trashTime := currentTime().Add(-trashRetention - time.Hour)
	if err := db.Delete(&book).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Unscoped().Model(&models.Book{}).Where("id = ?", book.ID).Updates(map[string]any{
		"deleted_at": trashTime, "trash_group": "maintenance-test",
	}).Error; err != nil {
		t.Fatal(err)
	}
	expiredDay := analyticsDayStart(currentTime()).AddDate(0, 0, -bookAnalyticsRetentionDays).Format("2006-01-02")
	if err := db.Create(&models.BookAnalyticsDaily{BookID: book.ID, DocumentID: doc.ID, Day: expiredDay, Source: "direct", ViewCount: 1}).Error; err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(dataDir, "import-jobs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	staleSource := filepath.Join(dir, "pdf-maintenance.pdf")
	if err := os.WriteFile(staleSource, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := currentTime().Add(-importSourceRetention - time.Hour)
	if err := os.Chtimes(staleSource, old, old); err != nil {
		t.Fatal(err)
	}

	a.enqueueMaintenanceIfDue(context.Background(), a.Jobs)
	a.enqueueMaintenanceIfDue(context.Background(), a.Jobs)
	var queued int64
	if err := db.Model(&models.BackgroundJob{}).Where("type = ?", maintenanceJobType).Count(&queued).Error; err != nil || queued != 1 {
		t.Fatalf("maintenance task must be queued once: count=%d err=%v", queued, err)
	}
	if ran, runErr := a.Jobs.RunOnce(context.Background()); !ran || runErr != nil {
		t.Fatalf("maintenance task failed: ran=%v err=%v", ran, runErr)
	}

	if err := db.Unscoped().First(&models.Book{}, book.ID).Error; err == nil {
		t.Fatal("expired trashed book was not permanently deleted")
	}
	var analyticsCount int64
	if err := db.Model(&models.BookAnalyticsDaily{}).Where("day = ?", expiredDay).Count(&analyticsCount).Error; err != nil || analyticsCount != 0 {
		t.Fatalf("expired analytics were not deleted: count=%d err=%v", analyticsCount, err)
	}
	if _, err := os.Stat(staleSource); !os.IsNotExist(err) {
		t.Fatalf("expired import source was not deleted: %v", err)
	}
	if err := a.runMaintenanceCleanup(context.Background(), nil); err != nil {
		t.Fatalf("repeated maintenance cleanup must be safe: %v", err)
	}
}

func TestAdminBackgroundJobs(t *testing.T) {
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

	request := func(method, path string, body any, token string) (int, map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		payload := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return resp.StatusCode, payload
	}

	_, installed := request(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "任务测试站"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	adminToken := installed["data"].(map[string]any)["token"].(string)
	_, registered := request(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "alice", "email": "alice@test.local", "password": "secret123",
	}, "")
	userToken := registered["data"].(map[string]any)["token"].(string)
	var alice models.User
	if err := a.DB.Where("username = ?", "alice").First(&alice).Error; err != nil {
		t.Fatal(err)
	}
	ownedJob, err := a.Jobs.EnqueueOwned(context.Background(), alice.ID, emailSendJobType,
		emailSendJob{To: "alice@test.local", Subject: "test", HTML: "<p>test</p>"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	ownedPath := "/api/v1/tasks/" + strconv.FormatUint(uint64(ownedJob.ID), 10)
	if status, _ := request(http.MethodGet, ownedPath, nil, userToken); status != http.StatusOK {
		t.Fatalf("task owner should read task: %d", status)
	}
	if status, _ := request(http.MethodGet, ownedPath, nil, adminToken); status != http.StatusNotFound {
		t.Fatalf("another user must not read task through owner endpoint: %d", status)
	}

	if status, _ := request(http.MethodGet, "/api/v1/admin/tasks", nil, ""); status != http.StatusUnauthorized {
		t.Fatalf("anonymous task list should be 401: %d", status)
	}
	if status, _ := request(http.MethodGet, "/api/v1/admin/tasks", nil, userToken); status != http.StatusForbidden {
		t.Fatalf("regular user task list should be 403: %d", status)
	}

	now := time.Now()
	job := models.BackgroundJob{
		Type: emailSendJobType, Payload: "must-not-leak@example.com", Status: jobqueue.StatusFailed,
		Attempts: 5, MaxAttempts: 5, AvailableAt: now, LastError: "SMTP unavailable",
		FinishedAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	if err := a.DB.Create(&job).Error; err != nil {
		t.Fatal(err)
	}

	status, listed := request(http.MethodGet, "/api/v1/admin/tasks?status=failed&type=email.send", nil, adminToken)
	if status != http.StatusOK {
		t.Fatalf("list tasks failed: %d %v", status, listed)
	}
	data := listed["data"].(map[string]any)
	items := data["items"].([]any)
	if len(items) != 1 || data["total"] != float64(1) {
		t.Fatalf("unexpected task list: %v", data)
	}
	encoded, _ := json.Marshal(listed)
	if strings.Contains(string(encoded), "must-not-leak@example.com") || strings.Contains(string(encoded), "payload") {
		t.Fatalf("task payload leaked from admin API: %s", encoded)
	}

	path := "/api/v1/admin/tasks/" + strconv.FormatUint(uint64(job.ID), 10) + "/retry"
	if status, payload := request(http.MethodPost, path, nil, adminToken); status != http.StatusOK {
		t.Fatalf("retry task failed: %d %v", status, payload)
	}
	if status, _ := request(http.MethodPost, path, nil, adminToken); status != http.StatusConflict {
		t.Fatalf("task must not be queued twice: %d", status)
	}
	var updated models.BackgroundJob
	if err := a.DB.First(&updated, job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.Status != jobqueue.StatusPending || updated.Attempts != 0 || updated.LastError != "" {
		t.Fatalf("unexpected retried task: %+v", updated)
	}
	var audit models.AuditLog
	if err := a.DB.Where("action = ? AND resource_id = ?", "task.retried", strconv.FormatUint(uint64(job.ID), 10)).First(&audit).Error; err != nil {
		t.Fatalf("retry audit missing: %v", err)
	}

	if status, _ := request(http.MethodGet, "/api/v1/admin/tasks?status=unknown", nil, adminToken); status != http.StatusBadRequest {
		t.Fatalf("invalid task status should be 400: %d", status)
	}
}
