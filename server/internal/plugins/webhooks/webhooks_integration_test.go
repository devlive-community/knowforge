package webhooks_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"knowforge/server/internal/app"
	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugins/webhooks"
	"knowforge/server/internal/testdb"
)

type received struct {
	event, delivery, timestamp, signature string
	body                                  []byte
}

// receiver 记录收到的投递；status 可切换以模拟失败。
type receiver struct {
	mu     sync.Mutex
	got    []received
	status int
}

func (r *receiver) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.got = append(r.got, received{req.Header.Get("X-KnowForge-Event"), req.Header.Get("X-KnowForge-Delivery"),
			req.Header.Get("X-KnowForge-Timestamp"), req.Header.Get("X-KnowForge-Signature"), body})
		status := r.status
		r.mu.Unlock()
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte("ok"))
	})
}

func (r *receiver) events() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []string{}
	for _, g := range r.got {
		out = append(out, g.event)
	}
	return out
}

func (r *receiver) last() received {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.got[len(r.got)-1]
}

func setup(t *testing.T) (*app.App, *httptest.Server, string) {
	t.Helper()
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Router())
	t.Cleanup(srv.Close)
	status, p := request(t, srv, "", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"Webhook"},"admin":{"username":"wh-admin","email":"wh-admin@test.local","password":"secret123"}}`)
	if status != http.StatusOK {
		t.Fatalf("安装失败: %d %v", status, p)
	}
	token := data(p)["token"].(string)
	if status, p := request(t, srv, token, http.MethodPost, "/api/v1/admin/plugins/webhooks/install", ""); status != http.StatusOK {
		t.Fatalf("启用插件失败: %d %v", status, p)
	}
	return a, srv, token
}

func request(t *testing.T, srv *httptest.Server, token, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
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

func data(p map[string]any) map[string]any { d, _ := p["data"].(map[string]any); return d }

// runJobs 执行所有到期任务；force 时先把重试中的任务提前到现在（跳过退避等待）。
func runJobs(t *testing.T, a *app.App, force bool) {
	t.Helper()
	for i := 0; i < 50; i++ {
		if force {
			a.DB.Model(&models.BackgroundJob{}).Where("status = ?", "retrying").Update("available_at", time.Now().Add(-time.Second))
		}
		ran, _ := a.Jobs.RunOnce(context.Background())
		if !ran {
			return
		}
	}
}

func TestWebhooks(t *testing.T) {
	t.Setenv("KNOWFORGE_WEBHOOK_ALLOW_PRIVATE", "true") // 接收端在本机
	a, srv, adminToken := setup(t)
	recv := &receiver{}
	hookSrv := httptest.NewServer(recv.handler())
	t.Cleanup(hookSrv.Close)

	author := &models.User{Username: "wh-author", Email: "wh-author@test.local", Role: "user", IsActive: true, EmailVerified: true}
	reader := &models.User{Username: "wh-reader", Email: "wh-reader@test.local", Role: "user", IsActive: true, EmailVerified: true}
	a.DB.Create(author)
	a.DB.Create(reader)
	authorToken, _ := auth.GenerateToken(a.Config.Secret, author.ID, author.Username, author.Role)
	readerToken, _ := auth.GenerateToken(a.Config.Secret, reader.ID, reader.Username, reader.Role)
	as := func(token, method, path, body string) (int, map[string]any) {
		return request(t, srv, token, method, path, body)
	}
	_, bookA := as(authorToken, http.MethodPost, "/api/v1/books", `{"title":"书 A","status":"published","is_public":true}`)
	_, bookB := as(authorToken, http.MethodPost, "/api/v1/books", `{"title":"书 B","status":"published","is_public":true}`)
	bookAID, bookBID := uint(data(bookA)["id"].(float64)), uint(data(bookB)["id"].(float64))
	_, readerBook := as(readerToken, http.MethodPost, "/api/v1/books", `{"title":"别人的书"}`)

	// 校验
	for _, body := range []string{
		`{"url":"ftp://x","events":["chapter.published"]}`,
		fmt.Sprintf(`{"url":%q,"events":[]}`, hookSrv.URL),
		fmt.Sprintf(`{"url":%q,"events":["book.deleted"]}`, hookSrv.URL),
		fmt.Sprintf(`{"url":%q,"events":["chapter.published"],"book_id":%v}`, hookSrv.URL, data(readerBook)["id"]),
	} {
		if status, _ := as(authorToken, http.MethodPost, "/api/v1/webhooks", body); status != http.StatusBadRequest {
			t.Fatalf("应拒绝 %s: %d", body, status)
		}
	}

	// 创建：订阅章节发布与评论（全部书籍）；密钥只在创建时返回
	status, p := as(authorToken, http.MethodPost, "/api/v1/webhooks", fmt.Sprintf(`{"url":%q,"events":["chapter.published","comment.received"]}`, hookSrv.URL))
	secret, _ := data(p)["secret"].(string)
	if status != http.StatusOK || !strings.HasPrefix(secret, "whsec_") {
		t.Fatalf("创建失败: %d %v", status, p)
	}
	hookID := uint(data(p)["item"].(map[string]any)["id"].(float64))
	_, list := as(authorToken, http.MethodGet, "/api/v1/webhooks", "")
	if item := data(list)["items"].([]any)[0].(map[string]any); item["secret"] != nil || data(list)["limit"].(float64) != 5 {
		t.Fatalf("列表不应返回密钥: %v", data(list))
	}
	// 只订阅书 B 的点赞
	as(authorToken, http.MethodPost, "/api/v1/webhooks", fmt.Sprintf(`{"url":%q,"events":["reaction.received"],"book_id":%d}`, hookSrv.URL+"/b", bookBID))

	// 发布章节 → chapter.published，签名可校验
	_, doc := as(authorToken, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookAID), `{"title":"第一章","content":"正文","status":"published"}`)
	docID := uint(data(doc)["id"].(float64))
	runJobs(t, a, false)
	if ev := recv.events(); len(ev) != 1 || ev[0] != "chapter.published" {
		t.Fatalf("应收到章节发布事件: %v", ev)
	}
	got := recv.last()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(got.timestamp + "."))
	mac.Write(got.body)
	if got.signature != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
		t.Fatalf("签名校验失败: %s", got.signature)
	}
	var payload struct {
		ID    uint   `json:"id"`
		Event string `json:"event"`
		Data  struct {
			Chapter struct{ Title string } `json:"chapter"`
			Book    struct{ Title string } `json:"book"`
		} `json:"data"`
	}
	_ = json.Unmarshal(got.body, &payload)
	if payload.Event != "chapter.published" || payload.Data.Chapter.Title != "第一章" || payload.Data.Book.Title != "书 A" || fmt.Sprint(payload.ID) != got.delivery {
		t.Fatalf("投递内容异常: %s", got.body)
	}

	// 评论 → comment.received；点赞书 A → 不投递（只订阅了书 B 的点赞）；点赞书 B → 投递到第二个订阅
	as(readerToken, http.MethodPost, fmt.Sprintf("/api/v1/documents/%d/comments", docID), `{"content":"写得好"}`)
	as(readerToken, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/reactions", bookAID), `{"type":"like"}`)
	as(readerToken, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/reactions", bookBID), `{"type":"like"}`)
	runJobs(t, a, false)
	if ev := recv.events(); strings.Join(ev, ",") != "chapter.published,comment.received,reaction.received" {
		t.Fatalf("事件与订阅范围不符: %v", ev)
	}

	// 测试投递与记录
	if status, _ := as(authorToken, http.MethodPost, fmt.Sprintf("/api/v1/webhooks/%d/test", hookID), ""); status != http.StatusOK {
		t.Fatalf("测试投递失败: %d", status)
	}
	runJobs(t, a, false)
	_, deliveries := as(authorToken, http.MethodGet, fmt.Sprintf("/api/v1/webhooks/%d/deliveries", hookID), "")
	first := data(deliveries)["items"].([]any)[0].(map[string]any)
	if first["event"] != "ping" || first["status"] != "success" || first["response_status"].(float64) != 200 || first["response_body"] != "ok" {
		t.Fatalf("投递记录异常: %v", first)
	}
	if status, _ := as(readerToken, http.MethodGet, fmt.Sprintf("/api/v1/webhooks/%d/deliveries", hookID), ""); status != http.StatusNotFound {
		t.Fatalf("不能查看别人的投递记录: %d", status)
	}

	// 失败重试：接收端返回 500，重试用尽后记为失败；重新投递成功
	recv.mu.Lock()
	recv.status = http.StatusInternalServerError
	recv.mu.Unlock()
	as(authorToken, http.MethodPost, fmt.Sprintf("/api/v1/webhooks/%d/test", hookID), "")
	runJobs(t, a, true)
	var failed webhooks.Delivery
	a.DB.Where("hook_id = ? AND event = ?", hookID, "ping").Order("id DESC").First(&failed)
	if failed.Status != "failed" || failed.Attempts != 5 || failed.ResponseStatus != 500 {
		t.Fatalf("应重试 5 次后记为失败: %+v", failed)
	}
	recv.mu.Lock()
	recv.status = http.StatusOK
	recv.mu.Unlock()
	if status, _ := as(authorToken, http.MethodPost, fmt.Sprintf("/api/v1/webhooks/deliveries/%d/redeliver", failed.ID), ""); status != http.StatusOK {
		t.Fatalf("重新投递失败: %d", status)
	}
	runJobs(t, a, false)
	var hook webhooks.Hook
	a.DB.First(&hook, hookID)
	if hook.Failures != 0 || hook.LastStatus != "success" {
		t.Fatalf("成功投递后应清零连续失败: %+v", hook)
	}

	// 连续失败过多：自动停用并通知
	a.DB.Model(&webhooks.Hook{}).Where("id = ?", hookID).Update("failures", 9)
	recv.mu.Lock()
	recv.status = http.StatusBadGateway
	recv.mu.Unlock()
	as(authorToken, http.MethodPost, fmt.Sprintf("/api/v1/webhooks/%d/test", hookID), "")
	runJobs(t, a, true)
	a.DB.First(&hook, hookID)
	var notices int64
	a.DB.Model(&models.Notification{}).Where("user_id = ? AND title LIKE ?", author.ID, "%已自动停用%").Count(&notices)
	if hook.Active || hook.DisabledReason == "" || notices != 1 {
		t.Fatalf("连续失败后应停用并通知: %+v %d", hook, notices)
	}
	// 停用期间不投递；重新启用后清空失败计数
	count := len(recv.events())
	as(authorToken, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookAID), `{"title":"第二章","content":"x","status":"published"}`)
	runJobs(t, a, false)
	if len(recv.events()) != count {
		t.Fatal("停用的订阅不应投递")
	}
	if _, p := as(authorToken, http.MethodPut, fmt.Sprintf("/api/v1/webhooks/%d", hookID), `{"active":true}`); data(p)["failures"].(float64) != 0 || data(p)["disabled_reason"] != "" {
		t.Fatalf("重新启用后应清空: %v", p)
	}

	// 重置密钥
	if _, p := as(authorToken, http.MethodPost, fmt.Sprintf("/api/v1/webhooks/%d/secret", hookID), ""); data(p)["secret"] == secret {
		t.Fatal("重置后密钥应变化")
	}

	// 默认不允许投递到内网：创建时拒绝本机地址，已有订阅投递时拒绝且不重试
	t.Setenv("KNOWFORGE_WEBHOOK_ALLOW_PRIVATE", "false")
	if status, p := as(authorToken, http.MethodPost, "/api/v1/webhooks", `{"url":"http://127.0.0.1:9/x","events":["chapter.published"]}`); status != http.StatusBadRequest || !strings.Contains(p["message"].(string), "内网") {
		t.Fatalf("应拒绝内网地址: %d %v", status, p)
	}
	as(authorToken, http.MethodPost, fmt.Sprintf("/api/v1/webhooks/%d/test", hookID), "")
	runJobs(t, a, false)
	var blocked webhooks.Delivery
	a.DB.Where("hook_id = ?", hookID).Order("id DESC").First(&blocked)
	if blocked.Status != "failed" || blocked.Attempts != 1 || !strings.Contains(blocked.Error, "内网") {
		t.Fatalf("投递到内网应直接失败: %+v", blocked)
	}

	// 数量上限为权益
	request(t, srv, adminToken, http.MethodPut, "/api/v1/admin/entitlements/base", `{"values":{"webhooks.max":2}}`)
	t.Setenv("KNOWFORGE_WEBHOOK_ALLOW_PRIVATE", "true")
	if status, _ := as(authorToken, http.MethodPost, "/api/v1/webhooks", fmt.Sprintf(`{"url":%q,"events":["chapter.published"]}`, hookSrv.URL)); status != http.StatusForbidden {
		t.Fatalf("超出上限应拒绝: %d", status)
	}
}
