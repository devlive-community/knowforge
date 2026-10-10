package indexnow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"knowforge/server/internal/jobqueue"
	"knowforge/server/internal/plugincore"
)

type testCore struct {
	plugincore.Core
	db      *gorm.DB
	enabled bool
	siteURL string
}

func (c *testCore) Gorm() *gorm.DB            { return c.db }
func (c *testCore) PluginEnabled(string) bool { return c.enabled }
func (c *testCore) JobQueue() *jobqueue.Queue { return nil }
func (c *testCore) OK(g *gin.Context, data any) {
	g.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}
func (c *testCore) Fail(g *gin.Context, status int, message string) {
	g.JSON(status, gin.H{"success": false, "message": message})
}
func (c *testCore) GetSetting(key string) string {
	if key == "site_url" {
		return c.siteURL
	}
	return ""
}

func newTestCore(t *testing.T, siteURL string) *testCore {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&Config{}, &URL{}, &PushLog{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Config{ID: 1, Key: "0123456789abcdef0123456789abcdef"}).Error; err != nil {
		t.Fatal(err)
	}
	return &testCore{db: db, enabled: true, siteURL: siteURL}
}

func TestValidateSetupRequiresRootHTTPSURL(t *testing.T) {
	for _, raw := range []string{"https://example.com", "https://example.com/"} {
		if err := validateSetup(raw); err != nil {
			t.Errorf("validateSetup(%q): %v", raw, err)
		}
	}
	for _, raw := range []string{
		"http://example.com", "https://example.com/path", "https://user@example.com",
		"https://example.com?x=1", "https://example.com/#fragment", "not a url",
	} {
		if err := validateSetup(raw); err == nil {
			t.Errorf("validateSetup(%q) unexpectedly succeeded", raw)
		}
	}
}

func TestValidSubmittedURLRestrictsHostSchemeAndPath(t *testing.T) {
	base, err := canonicalSiteURL("https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"https://example.com/book/detail/a-book",
		"https://EXAMPLE.com/book/reader/a-book/chapter-1",
	} {
		if !validSubmittedURL(raw, base) {
			t.Errorf("validSubmittedURL(%q) = false", raw)
		}
	}
	for _, raw := range []string{
		"http://example.com/book/detail/a-book",
		"https://evil.example/book/detail/a-book",
		"https://example.com/book/detail/a-book?preview=1",
		"https://example.com/book/detail/a-book#section",
		"https://example.com/admin/users",
		"https://example.com/book/detail/\\evil",
		"https://example.com/book/detail/" + strings.Repeat("a", maxStoredURLLen),
	} {
		if validSubmittedURL(raw, base) {
			t.Errorf("validSubmittedURL(%q) = true", raw)
		}
	}
}

func TestRecordAndEnqueueCoalescesURLsAndRejectsOutOfScope(t *testing.T) {
	core := newTestCore(t, "https://example.com")
	b := &behavior{core: core}
	valid := "https://example.com/book/detail/a-book"
	b.recordAndEnqueue([]string{
		valid, valid,
		"http://example.com/book/detail/insecure",
		"https://other.example/book/detail/external",
		"https://example.com/admin/users",
	})
	if err := core.db.Model(&URL{}).Where("address = ?", valid).Updates(map[string]any{"attempts": maxAttempts, "last_error": "previous error"}).Error; err != nil {
		t.Fatal(err)
	}
	b.recordAndEnqueue([]string{valid})

	var rows []URL
	if err := core.db.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one coalesced URL row, got %d", len(rows))
	}
	if rows[0].Address != valid || rows[0].Version != 2 || rows[0].SubmittedVersion != 0 || rows[0].Attempts != 0 || rows[0].LastError != "" {
		t.Fatalf("unexpected queued URL state: %+v", rows[0])
	}
}

func TestPostSendsIndexNowPayloadAndAcceptsProtocolSuccess(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusAccepted} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json; charset=utf-8" {
					t.Errorf("unexpected request method or content type: %s %q", r.Method, r.Header.Get("Content-Type"))
				}
				var payload indexNowRequest
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Errorf("decode payload: %v", err)
				}
				if payload.Host != "example.com" || payload.Key != "test-key" || payload.KeyLocation != "https://example.com/test-key.txt" || len(payload.URLList) != 1 {
					t.Errorf("unexpected payload: %+v", payload)
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			base, _ := canonicalSiteURL("https://example.com")
			if err := post(context.Background(), server.URL, base, "test-key", []string{"https://example.com/book/detail/a-book"}); err != nil {
				t.Fatalf("post returned error for HTTP %d: %v", status, err)
			}
		})
	}
}

func TestPostReturnsProtocolRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	base, _ := canonicalSiteURL("https://example.com")
	err := post(context.Background(), server.URL, base, "test-key", []string{"https://example.com/book/detail/a-book"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("expected HTTP 403 error, got %v", err)
	}
}

func TestSubmitDiscardsQueuedURLsFromPreviousHost(t *testing.T) {
	core := newTestCore(t, "https://new.example")
	row := URL{Address: "https://old.example/book/detail/a-book", AddressHash: "old-host-hash", Version: 1}
	if err := core.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	oldEndpoint := endpointURL
	endpointURL = server.URL
	t.Cleanup(func() { endpointURL = oldEndpoint })

	b := &behavior{core: core}
	if err := b.submitPending(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	var got URL
	if err := core.db.First(&got, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 || got.SubmittedVersion != got.Version || got.LockedAt != nil {
		t.Fatalf("old-host URL must not be sent to the new site key: requests=%d row=%+v", requests.Load(), got)
	}
}

func TestPurgeSubmittedRemovesOnlyExpiredCompletedRows(t *testing.T) {
	core := newTestCore(t, "https://example.com")
	now := time.Now().UTC()
	rows := []URL{
		{Address: "https://example.com/book/detail/expired", AddressHash: "expired", Version: 1, SubmittedVersion: 1, LastSubmittedAt: timePtr(now.Add(-submissionKeep - time.Hour))},
		{Address: "https://example.com/book/detail/recent", AddressHash: "recent", Version: 1, SubmittedVersion: 1, LastSubmittedAt: timePtr(now.Add(-time.Hour))},
		{Address: "https://example.com/book/detail/pending", AddressHash: "pending", Version: 2, SubmittedVersion: 1, LastSubmittedAt: timePtr(now.Add(-submissionKeep - time.Hour))},
	}
	if err := core.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	(&behavior{core: core}).purgeSubmitted(context.Background(), now)
	var remaining []URL
	if err := core.db.Order("id ASC").Find(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 2 || remaining[0].AddressHash != "recent" || remaining[1].AddressHash != "pending" {
		t.Fatalf("unexpected retained submissions: %+v", remaining)
	}
}

func timePtr(value time.Time) *time.Time { return &value }

func TestSubmitPreservesURLChangesReceivedDuringRequest(t *testing.T) {
	core := newTestCore(t, "https://example.com")
	address := "https://example.com/book/detail/a-book"
	row := URL{Address: address, AddressHash: "hash", Version: 1, UpdatedAt: time.Now().UTC()}
	if err := core.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := core.db.Model(&URL{}).Where("id = ?", row.ID).Update("version", 2).Error; err != nil {
			t.Errorf("simulate concurrent URL update: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	oldEndpoint := endpointURL
	endpointURL = server.URL
	t.Cleanup(func() { endpointURL = oldEndpoint })

	b := &behavior{core: core}
	if err := b.submitPending(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	var got URL
	if err := core.db.First(&got, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Version != 2 || got.SubmittedVersion != 1 || got.LockedAt != nil {
		t.Fatalf("in-flight content change was lost or left locked: %+v", got)
	}
}

// MySQL（DATETIME(3)）与 PostgreSQL 会截断 locked_at 的纳秒部分：推送成功或失败后的更新都不能依赖时间相等，
// 否则已推送的 URL 会一直处于待推送并在锁过期后被反复提交，失败也不会计入重试次数。
func TestSubmitSurvivesDatabaseTimePrecisionLoss(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{{"success", http.StatusOK}, {"failure", http.StatusInternalServerError}} {
		t.Run(tc.name, func(t *testing.T) {
			core := newTestCore(t, "https://example.com")
			row := URL{Address: "https://example.com/book/detail/a-book", AddressHash: "hash", Version: 1, UpdatedAt: time.Now().UTC()}
			if err := core.db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				// 模拟数据库按毫秒保存认领时间
				var claimed URL
				core.db.First(&claimed, row.ID)
				if claimed.LockedAt == nil {
					t.Error("URL should be claimed during the request")
				} else {
					core.db.Model(&URL{}).Where("id = ?", row.ID).Update("locked_at", claimed.LockedAt.Truncate(time.Millisecond).Add(-time.Microsecond))
				}
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			oldEndpoint := endpointURL
			endpointURL = server.URL
			t.Cleanup(func() { endpointURL = oldEndpoint })

			err := (&behavior{core: core}).submitPending(context.Background(), nil)
			var got URL
			core.db.First(&got, row.ID)
			if got.LockedAt != nil || got.ClaimToken != "" {
				t.Fatalf("claim must be released: %+v", got)
			}
			if tc.status == http.StatusOK && (err != nil || got.SubmittedVersion != 1 || got.LastSubmittedAt == nil) {
				t.Fatalf("successful push must mark the URL submitted: err=%v row=%+v", err, got)
			}
			if tc.status != http.StatusOK && (err == nil || got.Attempts != 1 || got.SubmittedVersion != 0) {
				t.Fatalf("failed push must count an attempt: err=%v row=%+v", err, got)
			}
		})
	}
}

func TestValidSubmittedURLAllowsCategoryPages(t *testing.T) {
	base, _ := canonicalSiteURL("https://example.com")
	if !validSubmittedURL("https://example.com/explore?category=go", base) {
		t.Fatal("category page should be submittable")
	}
	for _, raw := range []string{"https://example.com/explore", "https://example.com/explore?category=go&page=2", "https://example.com/explore?tag=go", "https://example.com/book/detail/a?x=1"} {
		if validSubmittedURL(raw, base) {
			t.Errorf("validSubmittedURL(%q) unexpectedly succeeded", raw)
		}
	}
}

func fakeEndpoint(t *testing.T, status int) *atomic.Int32 {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	old := endpointURL
	endpointURL = server.URL
	t.Cleanup(func() { endpointURL = old })
	return &requests
}

// 访问时推送：立即推送并把队列中同一地址记为已推送；间隔内不重复推送；每次推送都有记录。
func TestPushNowSkipsRecentlySubmittedAndCompletesQueue(t *testing.T) {
	core := newTestCore(t, "https://example.com")
	requests := fakeEndpoint(t, http.StatusOK)
	address := "https://example.com/book/detail/a-book"
	queued := URL{Address: address, AddressHash: addressHash(address), Version: 3, SubmittedVersion: 1, UpdatedAt: time.Now().UTC()}
	core.db.Create(&queued)
	b := &behavior{core: core}
	if n, err := b.pushNow(context.Background(), sourceVisit, []string{address}, time.Hour); err != nil || n != 1 {
		t.Fatalf("first visit should push: n=%d err=%v", n, err)
	}
	var got URL
	core.db.First(&got, queued.ID)
	if got.SubmittedVersion != 3 || got.LastSubmittedAt == nil {
		t.Fatalf("queued URL should be completed: %+v", got)
	}
	if n, _ := b.pushNow(context.Background(), sourceVisit, []string{address}, time.Hour); n != 0 || requests.Load() != 1 {
		t.Fatalf("visit within interval must not push again: n=%d requests=%d", n, requests.Load())
	}
	other := "https://example.com/book/reader/a-book/one"
	if n, _ := b.pushNow(context.Background(), sourceManual, []string{other}, 0); n != 1 {
		t.Fatal("manual push of a new URL should be sent")
	}
	var logs []PushLog
	core.db.Order("id ASC").Find(&logs)
	if len(logs) != 2 || logs[0].Source != sourceVisit || logs[1].Source != sourceManual || logs[1].Status != "ok" {
		t.Fatalf("push logs: %+v", logs)
	}
}

func TestManualURLAcceptsSitePathsOnly(t *testing.T) {
	base, _ := canonicalSiteURL("https://example.com")
	if u, ok := manualURL("/book/detail/a", base); !ok || u != "https://example.com/book/detail/a" {
		t.Fatalf("site path: %q %v", u, ok)
	}
	if u, ok := manualURL("https://EXAMPLE.com/explore?category=go", base); !ok || u != "https://EXAMPLE.com/explore?category=go" {
		t.Fatalf("absolute same-host: %q %v", u, ok)
	}
	for _, raw := range []string{"https://other.com/a", "http://example.com/a", "//other.com/a", "https://example.com/a#x"} {
		if _, ok := manualURL(raw, base); ok {
			t.Errorf("manualURL(%q) unexpectedly accepted", raw)
		}
	}
}

// 触发地址：密钥错误 404；正确时立即推送积压并返回剩余数；短时间内重复触发 429。
func TestTriggerFlushesPendingWithToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core := newTestCore(t, "https://example.com")
	core.db.Model(&Config{}).Where("id = 1").Update("trigger_token", "secret-token")
	requests := fakeEndpoint(t, http.StatusOK)
	for i, path := range []string{"/book/detail/a", "/book/detail/b"} {
		raw := "https://example.com" + path
		core.db.Create(&URL{Address: raw, AddressHash: addressHash(raw), Version: 1, UpdatedAt: time.Now().UTC().Add(time.Duration(i) * time.Second)})
	}
	b := &behavior{core: core}
	r := gin.New()
	r.GET("/trigger", b.trigger)
	call := func(token string) (int, string) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/trigger?token="+token, nil))
		return w.Code, w.Body.String()
	}
	triggerGate.last = time.Time{}
	if code, _ := call("wrong"); code != http.StatusNotFound {
		t.Fatalf("wrong token: %d", code)
	}
	code, body := call("secret-token")
	if code != http.StatusOK || !strings.Contains(body, `"submitted":2`) || !strings.Contains(body, `"remaining":0`) || requests.Load() != 1 {
		t.Fatalf("trigger should flush pending: %d %s requests=%d", code, body, requests.Load())
	}
	if code, _ := call("secret-token"); code != http.StatusTooManyRequests {
		t.Fatalf("repeated trigger should be throttled: %d", code)
	}
	var log PushLog
	core.db.Last(&log)
	if log.Source != sourceTrigger || log.URLCount != 2 {
		t.Fatalf("trigger log: %+v", log)
	}
}
