package webhooks_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"knowforge/server/internal/auth"
	"knowforge/server/internal/models"
)

// 来自其他插件的事件：作品被购买（付费内容）、书籍收到公开提问（书籍问答）、自己的会员变化（会员），
// 经业务活动转发，投递内容带书籍/章节与各自的详情。
func TestWebhookPluginEvents(t *testing.T) {
	t.Setenv("KNOWFORGE_WEBHOOK_ALLOW_PRIVATE", "true")
	a, srv, adminToken := setup(t)
	recv := &receiver{}
	hookSrv := httptest.NewServer(recv.handler())
	t.Cleanup(hookSrv.Close)
	for _, key := range []string{"payment", "paid-content", "qa", "membership"} {
		if status, p := request(t, srv, adminToken, http.MethodPost, "/api/v1/admin/plugins/"+key+"/install", ""); status != http.StatusOK {
			t.Fatalf("启用 %s 失败: %d %v", key, status, p)
		}
	}
	request(t, srv, adminToken, http.MethodPut, "/api/v1/admin/payment/settings", `{"offline_enabled":true,"offline_instructions":"转账"}`)

	author := &models.User{Username: "ev-author", Email: "ev-author@test.local", Role: "user", IsActive: true, EmailVerified: true}
	reader := &models.User{Username: "ev-reader", Nickname: "读者乙", Email: "ev-reader@test.local", Role: "user", IsActive: true, EmailVerified: true}
	a.DB.Create(author)
	a.DB.Create(reader)
	authorToken, _ := auth.GenerateToken(a.Config.Secret, author.ID, author.Username, author.Role)
	readerToken, _ := auth.GenerateToken(a.Config.Secret, reader.ID, reader.Username, reader.Role)
	as := func(token, method, path, body string) (int, map[string]any) {
		return request(t, srv, token, method, path, body)
	}

	if status, p := as(authorToken, http.MethodPost, "/api/v1/webhooks", fmt.Sprintf(`{"url":%q,"events":["sale.completed","question.received","membership.changed"]}`, hookSrv.URL)); status != http.StatusOK {
		t.Fatalf("订阅失败: %d %v", status, p)
	}
	_, book := as(authorToken, http.MethodPost, "/api/v1/books", `{"title":"付费书","status":"published","is_public":true}`)
	bookID := uint(data(book)["id"].(float64))
	_, doc := as(authorToken, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), `{"title":"付费章","content":"正文内容。","status":"published"}`)
	docID := uint(data(doc)["id"].(float64))
	as(authorToken, http.MethodPut, fmt.Sprintf("/api/v1/books/%d/paid-settings", bookID), `{"enabled":true,"chapter_price_cents":300,"free_chapters":0,"preview_percent":10}`)

	// 读者购买章节 → sale.completed
	status, p := as(readerToken, http.MethodPost, "/api/v1/payment/orders", fmt.Sprintf(`{"kind":"paid-doc","sku":"%d","channel":"offline"}`, docID))
	if status != http.StatusOK {
		t.Fatalf("下单失败: %d %v", status, p)
	}
	orderNo := data(p)["order"].(map[string]any)["order_no"].(string)
	if status, p := as(adminToken, http.MethodPost, "/api/v1/admin/payment/orders/"+orderNo+"/confirm", ""); status != http.StatusOK {
		t.Fatalf("确认收款失败: %d %v", status, p)
	}
	// 读者提问 → question.received
	if status, p := as(readerToken, http.MethodPost, fmt.Sprintf("/api/v1/qa/books/%d/questions", bookID), fmt.Sprintf(`{"title":"如何配置？","body":"正文","doc_id":%d}`, docID)); status != http.StatusOK {
		t.Fatalf("提问失败: %d %v", status, p)
	}
	// 管理员为作者开通会员 → membership.changed
	_, plan := as(adminToken, http.MethodPost, "/api/v1/admin/membership/plans", `{"name":"专业版","prices":[{"duration_days":30,"price_cents":1000}]}`)
	if status, p := as(adminToken, http.MethodPost, "/api/v1/admin/membership/grant", fmt.Sprintf(`{"user_id":%d,"plan_id":%v,"days":30}`, author.ID, data(plan)["id"])); status != http.StatusOK {
		t.Fatalf("开通会员失败: %d %v", status, p)
	}
	runJobs(t, a, false)

	byEvent := map[string]map[string]any{}
	recv.mu.Lock()
	for _, g := range recv.got {
		var body struct {
			Data map[string]any `json:"data"`
		}
		_ = json.Unmarshal(g.body, &body)
		byEvent[g.event] = body.Data
	}
	recv.mu.Unlock()
	if len(byEvent) != 3 {
		t.Fatalf("应收到 3 种事件: %v", recv.events())
	}
	sale := byEvent["sale.completed"]
	s := sale["sale"].(map[string]any)
	if sale["book"].(map[string]any)["title"] != "付费书" || sale["chapter"].(map[string]any)["title"] != "付费章" || s["order_no"] != orderNo || s["amount_cents"].(float64) != 300 || s["net_cents"] == nil {
		t.Fatalf("售出事件内容异常: %v", sale)
	}
	q := byEvent["question.received"]["question"].(map[string]any)
	if q["title"] != "如何配置？" || q["author"].(map[string]any)["username"] != "ev-reader" || !strings.Contains(q["url"].(string), "/book/detail/") {
		t.Fatalf("提问事件内容异常: %v", byEvent["question.received"])
	}
	m := byEvent["membership.changed"]
	if m["status"] != "active" || m["plan"].(map[string]any)["name"] != "专业版" || m["expires_at"] == "" {
		t.Fatalf("会员事件内容异常: %v", m)
	}
}
