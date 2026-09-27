package payment_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"knowforge/server/internal/app"
	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins/payment"
	"knowforge/server/internal/testdb"
)

// 集成测试：启动完整应用、启用支付插件，以测试内登记的商品验证下单 → 支付确认 → 履约（幂等）全流程。

var (
	fulfilledMu    sync.Mutex
	fulfilled      = map[string]int{} // 订单号 → 履约次数
	failNext       bool
	refundEvents   = map[string][]plugincore.RefundEvent{} // 订单号 → 收到的退款回调（按退款单号幂等）
	failRefundNext bool
)

func init() {
	plugincore.RegisterProductProvider(plugincore.ProductProvider{
		Kind: "test-item",
		Resolve: func(_ plugincore.Core, _ *models.User, sku string) (plugincore.Product, error) {
			if sku == "gone" {
				return plugincore.Product{}, errors.New("商品已下架")
			}
			return plugincore.Product{Kind: "test-item", SKU: sku, Title: "测试商品 " + sku, AmountCents: 990, Currency: "CNY", ReturnLink: "/somewhere",
				Payload: map[string]any{"sku": sku}}, nil
		},
		Fulfill: func(_ plugincore.Core, _ uint, orderNo string, payload map[string]any) error {
			fulfilledMu.Lock()
			defer fulfilledMu.Unlock()
			if failNext {
				failNext = false
				return errors.New("临时故障")
			}
			if payload["sku"] == nil {
				return errors.New("快照丢失")
			}
			fulfilled[orderNo]++
			return nil
		},
		Refund: func(_ plugincore.Core, ev plugincore.RefundEvent) error {
			fulfilledMu.Lock()
			defer fulfilledMu.Unlock()
			if failRefundNext {
				failRefundNext = false
				return errors.New("临时故障")
			}
			for _, prev := range refundEvents[ev.OrderNo] {
				if prev.RefundNo == ev.RefundNo {
					return nil
				}
			}
			refundEvents[ev.OrderNo] = append(refundEvents[ev.OrderNo], ev)
			return nil
		},
	})
}

type testEnv struct {
	app    *app.App
	db     *gorm.DB
	token  string
	server *httptest.Server
	client *http.Client
}

func newTestEnv(t *testing.T) *testEnv {
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
	e := &testEnv{app: a, server: httptest.NewServer(a.Router()), client: &http.Client{Timeout: 10 * time.Second}}
	t.Cleanup(e.server.Close)
	status, installed := e.request(t, "", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"支付测试"},"admin":{"username":"pay-admin","email":"pay-admin@test.local","password":"secret123"}}`)
	if status != http.StatusOK {
		t.Fatalf("安装失败: %d %v", status, installed)
	}
	e.token = installed["data"].(map[string]any)["token"].(string)
	e.db = a.DB
	if status, payload := e.do(t, http.MethodPost, "/api/v1/admin/plugins/payment/install", ""); status != http.StatusOK {
		t.Fatalf("启用支付插件失败: %d %v", status, payload)
	}
	return e
}

func (e *testEnv) request(t *testing.T, token, method, path, body string, headers ...string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, e.server.URL+path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	payload := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&payload)
	return resp.StatusCode, payload
}

func (e *testEnv) do(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	return e.request(t, e.token, method, path, body)
}

func (e *testEnv) as(t *testing.T, u *models.User, method, path, body string) (int, map[string]any) {
	t.Helper()
	token, _ := auth.GenerateToken(e.app.Config.Secret, u.ID, u.Username, u.Role)
	return e.request(t, token, method, path, body)
}

func (e *testEnv) user(t *testing.T, name string) *models.User {
	t.Helper()
	u := &models.User{Username: name, Email: name + "@test.local", Role: "user", IsActive: true, EmailVerified: true}
	if err := e.db.Create(u).Error; err != nil {
		t.Fatal(err)
	}
	return u
}

func data(p map[string]any) map[string]any { d, _ := p["data"].(map[string]any); return d }

func fulfilledCount(no string) int {
	fulfilledMu.Lock()
	defer fulfilledMu.Unlock()
	return fulfilled[no]
}

func (e *testEnv) order(t *testing.T, no string) payment.Order {
	t.Helper()
	var o payment.Order
	e.db.Where("order_no = ?", no).First(&o)
	return o
}

// 线下转账：下单 → 提交付款说明 → 管理员确认 → 履约一次；重复确认 409；取消与过期。
func TestOfflinePaymentFlow(t *testing.T) {
	e := newTestEnv(t)
	u := e.user(t, "buyer")
	other := e.user(t, "other")

	// 未配置任何支付方式：公开配置为空、下单失败
	_, site := e.request(t, "", http.MethodGet, "/api/v1/site", "")
	if chs := data(site)["payment_channels"].([]any); len(chs) != 0 || data(site)["checkout_enabled"] != false {
		t.Fatalf("未配置时不应有可用支付方式: %v", chs)
	}
	if status, _ := e.as(t, u, http.MethodPost, "/api/v1/payment/orders", `{"kind":"test-item","sku":"a","channel":"offline"}`); status != http.StatusBadRequest {
		t.Fatalf("支付方式不可用时应 400，实际 %d", status)
	}

	// 配置线下转账（密钥不回显、非法配置 400）
	if status, _ := e.do(t, http.MethodPut, "/api/v1/admin/payment/settings", `{"offline_enabled":true,"offline_instructions":"请转账至 6222 0000 0000","offline_expire_hours":48}`); status != http.StatusOK {
		t.Fatalf("保存设置失败: %d", status)
	}
	if status, _ := e.do(t, http.MethodPut, "/api/v1/admin/payment/settings", `{"alipay_private_key":"not-a-key"}`); status != http.StatusBadRequest {
		t.Fatalf("非法私钥应 400，实际 %d", status)
	}
	if status, _ := e.do(t, http.MethodPut, "/api/v1/admin/payment/settings", `{"stripe_secret_key":"sk_live_secret"}`); status != http.StatusOK {
		t.Fatal("保存密钥失败")
	}
	_, s := e.do(t, http.MethodGet, "/api/v1/admin/payment/settings", "")
	if _, leaked := data(s)["stripe_secret_key"]; leaked || data(s)["stripe_secret_key_set"] != true {
		t.Fatalf("密钥不应回显，只返回是否已配置: %v", data(s))
	}
	_, site = e.request(t, "", http.MethodGet, "/api/v1/site", "")
	if chs := data(site)["payment_channels"].([]any); len(chs) != 1 || chs[0] != "offline" || data(site)["checkout_enabled"] != true {
		t.Fatalf("公开配置应只有线下转账（Stripe 缺 Webhook 密钥不可用）: %v", chs)
	}

	// 结算页与下单
	if status, _ := e.as(t, u, http.MethodGet, "/api/v1/payment/products/test-item/gone", ""); status != http.StatusBadRequest {
		t.Fatalf("不可购买的商品应 400，实际 %d", status)
	}
	_, prod := e.as(t, u, http.MethodGet, "/api/v1/payment/products/test-item/a", "")
	if data(prod)["product"].(map[string]any)["amount_cents"].(float64) != 990 {
		t.Fatalf("结算页商品错误: %v", prod)
	}
	status, created := e.as(t, u, http.MethodPost, "/api/v1/payment/orders", `{"kind":"test-item","sku":"a","channel":"offline"}`)
	if status != http.StatusOK || data(created)["action"].(map[string]any)["type"] != "offline" {
		t.Fatalf("下单失败: %d %v", status, created)
	}
	no := data(created)["order"].(map[string]any)["order_no"].(string)
	if o := e.order(t, no); o.Status != payment.StatusPending || o.AmountCents != 990 || time.Until(o.ExpiresAt) < 47*time.Hour {
		t.Fatalf("订单快照错误: %+v", o)
	}
	if status, _ := e.as(t, other, http.MethodGet, "/api/v1/payment/orders/"+no, ""); status != http.StatusNotFound {
		t.Fatalf("他人订单应 404，实际 %d", status)
	}
	if status, _ := e.as(t, u, http.MethodPost, "/api/v1/payment/orders/"+no+"/proof", `{"note":"已转账，尾号 1234"}`); status != http.StatusOK {
		t.Fatal("提交付款说明失败")
	}

	// 管理员确认 → 已支付、履约一次、通知
	if status, payload := e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no+"/confirm", ""); status != http.StatusOK {
		t.Fatalf("确认失败: %d %v", status, payload)
	}
	o := e.order(t, no)
	if o.Status != payment.StatusPaid || o.FulfilledAt == nil || o.PayerNote != "已转账，尾号 1234" || fulfilledCount(no) != 1 {
		t.Fatalf("确认后应已支付且履约一次: %+v %d", o, fulfilledCount(no))
	}
	if status, _ := e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no+"/confirm", ""); status != http.StatusConflict || fulfilledCount(no) != 1 {
		t.Fatal("重复确认应 409 且不重复履约")
	}
	var notices int64
	e.db.Model(&models.Notification{}).Where("user_id = ? AND type = ?", u.ID, "payment").Count(&notices)
	if notices != 1 {
		t.Fatalf("支付成功应通知一次，实际 %d", notices)
	}

	// 履约失败 → 记录错误 → 管理员重试成功
	status, created = e.as(t, u, http.MethodPost, "/api/v1/payment/orders", `{"kind":"test-item","sku":"b","channel":"offline"}`)
	no2 := data(created)["order"].(map[string]any)["order_no"].(string)
	fulfilledMu.Lock()
	failNext = true
	fulfilledMu.Unlock()
	e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no2+"/confirm", "")
	if o := e.order(t, no2); o.Status != payment.StatusPaid || o.FulfilledAt != nil || o.FulfillError == "" {
		t.Fatalf("履约失败应保留已支付并记录错误: %+v", o)
	}
	if status, _ := e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no2+"/fulfill", ""); status != http.StatusOK || fulfilledCount(no2) != 1 {
		t.Fatalf("重试履约失败: %d", status)
	}

	// 取消与过期
	_, created = e.as(t, u, http.MethodPost, "/api/v1/payment/orders", `{"kind":"test-item","sku":"c","channel":"offline"}`)
	no3 := data(created)["order"].(map[string]any)["order_no"].(string)
	if status, _ := e.as(t, u, http.MethodPost, "/api/v1/payment/orders/"+no3+"/cancel", ""); status != http.StatusOK || e.order(t, no3).Status != payment.StatusCancelled {
		t.Fatal("取消失败")
	}
	if status, _ := e.as(t, u, http.MethodPost, "/api/v1/payment/orders/"+no3+"/cancel", ""); status != http.StatusConflict {
		t.Fatal("已取消的订单不能再取消")
	}
	_, list := e.as(t, u, http.MethodGet, "/api/v1/users/me/orders", "")
	if data(list)["total"].(float64) != 3 {
		t.Fatalf("我的订单应有 3 条: %v", data(list)["total"])
	}
	_, admin := e.do(t, http.MethodGet, "/api/v1/admin/payment/orders?status=paid", "")
	if data(admin)["total"].(float64) != 2 {
		t.Fatalf("已支付订单应有 2 条: %v", data(admin)["total"])
	}
}

// Stripe Webhook：验签后入账；金额不匹配拒绝；重复事件不重复履约；已过期订单收到支付仍入账。
func TestStripeWebhookSettlesOrder(t *testing.T) {
	e := newTestEnv(t)
	u := e.user(t, "stripe-buyer")
	stripe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"cs_1","url":"https://checkout.stripe.com/c/pay/cs_1","payment_status":"unpaid"}`))
	}))
	defer stripe.Close()
	payment.SetStripeAPIBase(t, stripe.URL)
	e.do(t, http.MethodPut, "/api/v1/admin/payment/settings", `{"stripe_enabled":true,"stripe_secret_key":"sk_test","stripe_webhook_secret":"whsec_1"}`)

	status, created := e.as(t, u, http.MethodPost, "/api/v1/payment/orders", `{"kind":"test-item","sku":"s","channel":"stripe"}`)
	if status != http.StatusOK || data(created)["action"].(map[string]any)["url"] != "https://checkout.stripe.com/c/pay/cs_1" {
		t.Fatalf("Stripe 下单失败: %d %v", status, created)
	}
	no := data(created)["order"].(map[string]any)["order_no"].(string)
	e.db.Model(&payment.Order{}).Where("order_no = ?", no).Update("status", payment.StatusExpired) // 用户在过期后才付款

	send := func(amount int) int {
		event := []byte(fmt.Sprintf(`{"type":"checkout.session.completed","data":{"object":{"id":"cs_1","payment_status":"paid","amount_total":%d,"currency":"cny","client_reference_id":"%s","payment_intent":"pi_1"}}}`, amount, no))
		mac := hmac.New(sha256.New, []byte("whsec_1"))
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		mac.Write([]byte(ts + "." + string(event)))
		req, _ := http.NewRequest(http.MethodPost, e.server.URL+"/api/v1/payment/notify/stripe", bytes.NewReader(event))
		req.Header.Set("Stripe-Signature", "t="+ts+",v1="+hex.EncodeToString(mac.Sum(nil)))
		resp, err := e.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if status := send(1); status != http.StatusBadRequest || e.order(t, no).Status == payment.StatusPaid {
		t.Fatalf("金额不匹配应拒绝入账，实际 %d", status)
	}
	if status := send(990); status != http.StatusOK {
		t.Fatalf("合法 Webhook 应 200，实际 %d", status)
	}
	if o := e.order(t, no); o.Status != payment.StatusPaid || o.ChannelTradeNo != "pi_1" || fulfilledCount(no) != 1 {
		t.Fatalf("Webhook 后应已支付并履约: %+v", o)
	}
	if status := send(990); status != http.StatusOK || fulfilledCount(no) != 1 {
		t.Fatal("重复事件应正常应答且不重复履约")
	}
	// 伪造签名
	req, _ := http.NewRequest(http.MethodPost, e.server.URL+"/api/v1/payment/notify/stripe", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Stripe-Signature", "t=1,v1=00")
	resp, _ := e.client.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("伪造签名应 400，实际 %d", resp.StatusCode)
	}
}

func refundsOf(no string) []plugincore.RefundEvent {
	fulfilledMu.Lock()
	defer fulfilledMu.Unlock()
	return append([]plugincore.RefundEvent(nil), refundEvents[no]...)
}

// paidOfflineOrder 下单（线下转账）并由管理员确认到账，返回订单号。
func (e *testEnv) paidOfflineOrder(t *testing.T, u *models.User, sku string) string {
	t.Helper()
	_, created := e.as(t, u, http.MethodPost, "/api/v1/payment/orders", fmt.Sprintf(`{"kind":"test-item","sku":%q,"channel":"offline"}`, sku))
	no := data(created)["order"].(map[string]any)["order_no"].(string)
	if status, p := e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no+"/confirm", ""); status != http.StatusOK {
		t.Fatalf("确认收款失败: %d %v", status, p)
	}
	return no
}

// 退款：用户申请 → 审核（部分退款）→ 再申请 → 驳回 → 管理员直接退剩余并撤销；超额与并发不会多退；回调失败可重试；申请期限。
func TestRefundFlow(t *testing.T) {
	e := newTestEnv(t)
	u, other := e.user(t, "buyer"), e.user(t, "other")
	e.do(t, http.MethodPut, "/api/v1/admin/payment/settings", `{"offline_enabled":true,"offline_instructions":"转账"}`)
	no := e.paidOfflineOrder(t, u, "r1")

	_, view := e.as(t, u, http.MethodGet, "/api/v1/payment/orders/"+no, "")
	if data(view)["can_request_refund"] != true || data(view)["refundable_cents"].(float64) != 990 {
		t.Fatalf("已支付订单应可申请退款: %v", view)
	}
	if status, _ := e.as(t, other, http.MethodPost, "/api/v1/payment/orders/"+no+"/refund-request", `{"reason":"x"}`); status != http.StatusNotFound {
		t.Fatalf("不能为他人订单申请退款: %d", status)
	}
	if status, _ := e.as(t, u, http.MethodPost, "/api/v1/payment/orders/"+no+"/refund-request", `{"reason":" "}`); status != http.StatusBadRequest {
		t.Fatalf("须填写退款原因: %d", status)
	}
	status, req := e.as(t, u, http.MethodPost, "/api/v1/payment/orders/"+no+"/refund-request", `{"reason":"买错了"}`)
	if status != http.StatusOK || data(req)["status"] != "requested" || data(req)["amount_cents"].(float64) != 990 {
		t.Fatalf("申请退款失败: %d %v", status, req)
	}
	if status, _ := e.as(t, u, http.MethodPost, "/api/v1/payment/orders/"+no+"/refund-request", `{"reason":"再来"}`); status != http.StatusConflict {
		t.Fatalf("已有处理中的退款时不能重复申请: %d", status)
	}
	var notes int64
	e.db.Model(&models.Notification{}).Where("user_id = ?", 1).Count(&notes)
	if notes == 0 {
		t.Fatal("管理员应收到退款申请通知")
	}
	_, list := e.do(t, http.MethodGet, "/api/v1/admin/payment/refunds", "")
	if data(list)["requested"].(float64) != 1 {
		t.Fatalf("待审核数异常: %v", list)
	}

	// 部分退款（审核时下调金额，不撤销商品）
	rid := uint(data(req)["id"].(float64))
	if status, _ := e.do(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/payment/refunds/%d/approve", rid), `{"amount_cents":1000,"revoke":false}`); status != http.StatusConflict {
		t.Fatalf("审核金额不能超过申请金额: %d", status)
	}
	status, ok := e.do(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/payment/refunds/%d/approve", rid), `{"amount_cents":500,"revoke":false,"note":"部分退"}`)
	if status != http.StatusOK || data(ok)["status"] != "succeeded" || data(ok)["settled_at"] == nil {
		t.Fatalf("线下订单审核通过应直接退款成功: %d %v", status, ok)
	}
	o := e.order(t, no)
	if o.Status != payment.StatusPaid || o.RefundedCents != 500 {
		t.Fatalf("部分退款后订单应仍为已支付: %+v", o)
	}
	if evs := refundsOf(no); len(evs) != 1 || evs[0].AmountCents != 500 || evs[0].TotalCents != 990 || evs[0].Revoke || evs[0].Payload["sku"] != "r1" {
		t.Fatalf("商品提供者回调异常: %+v", evs)
	}

	// 再申请剩余金额 → 驳回（释放额度）
	_, req2 := e.as(t, u, http.MethodPost, "/api/v1/payment/orders/"+no+"/refund-request", `{"reason":"剩下的也退"}`)
	if data(req2)["amount_cents"].(float64) != 490 {
		t.Fatalf("应申请剩余可退金额: %v", req2)
	}
	if status, _ := e.do(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/payment/refunds/%d/reject", uint(data(req2)["id"].(float64))), `{"note":"已超出使用范围"}`); status != http.StatusOK {
		t.Fatalf("驳回失败: %d", status)
	}

	// 管理员直接退款：超额拒绝；回调失败后重试；全额退完订单置为已退款
	if status, _ := e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no+"/refunds", `{"amount_cents":491,"reason":"补偿","revoke":true}`); status != http.StatusConflict {
		t.Fatalf("超过可退金额应拒绝: %d", status)
	}
	fulfilledMu.Lock()
	failRefundNext = true
	fulfilledMu.Unlock()
	status, direct := e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no+"/refunds", `{"amount_cents":490,"reason":"补偿","revoke":true}`)
	if status != http.StatusOK || data(direct)["status"] != "succeeded" || data(direct)["settled_at"] != nil || data(direct)["settle_error"] == "" {
		t.Fatalf("回调失败时应记录错误待重试: %d %v", status, direct)
	}
	if status, p := e.do(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/payment/refunds/%d/settle", uint(data(direct)["id"].(float64))), ""); status != http.StatusOK || data(p)["settled_at"] == nil {
		t.Fatalf("重试回调失败: %d %v", status, p)
	}
	o = e.order(t, no)
	if o.Status != payment.StatusRefunded || o.RefundedCents != 990 {
		t.Fatalf("全额退款后订单应为已退款: %+v", o)
	}
	if evs := refundsOf(no); len(evs) != 2 || !evs[1].Revoke || evs[1].AmountCents != 490 {
		t.Fatalf("第二次退款回调异常: %+v", evs)
	}
	if status, _ := e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no+"/confirm", ""); status != http.StatusConflict {
		t.Fatalf("已退款订单不能再确认收款: %d", status)
	}
	if fulfilledCount(no) != 1 {
		t.Fatalf("退款不应触发重新履约: %d", fulfilledCount(no))
	}
	var userNotes int64
	e.db.Model(&models.Notification{}).Where("user_id = ?", u.ID).Count(&userNotes)
	if userNotes < 4 { // 支付成功 + 两次退款成功 + 驳回
		t.Fatalf("用户通知数量异常: %d", userNotes)
	}

	// 并发直接退款：额度原子占用，不会超额
	no2 := e.paidOfflineOrder(t, u, "r2")
	var wg sync.WaitGroup
	succeeded := 0
	var mu sync.Mutex
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if status, _ := e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no2+"/refunds", `{"amount_cents":300,"reason":"并发","revoke":false}`); status == http.StatusOK {
				mu.Lock()
				succeeded++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if o2 := e.order(t, no2); succeeded > 3 || o2.RefundedCents != int64(succeeded)*300 || o2.RefundedCents > 990 {
		t.Fatalf("并发退款不应超额: 成功 %d 次，已退 %d", succeeded, o2.RefundedCents)
	}

	// 申请期限：设为 0 后不开放用户申请
	no3 := e.paidOfflineOrder(t, u, "r3")
	e.do(t, http.MethodPut, "/api/v1/admin/payment/settings", `{"refund_request_days":0}`)
	_, view3 := e.as(t, u, http.MethodGet, "/api/v1/payment/orders/"+no3, "")
	if data(view3)["can_request_refund"] == true || data(view3)["refund_blocked_reason"] == nil {
		t.Fatalf("期限为 0 时不应可申请: %v", view3)
	}
	if status, _ := e.as(t, u, http.MethodPost, "/api/v1/payment/orders/"+no3+"/refund-request", `{"reason":"x"}`); status != http.StatusConflict {
		t.Fatalf("不开放时申请应被拒绝: %d", status)
	}
	if status, _ := e.do(t, http.MethodPut, "/api/v1/admin/payment/settings", `{"refund_request_days":400}`); status != http.StatusBadRequest {
		t.Fatalf("期限越界应 400: %d", status)
	}
}
