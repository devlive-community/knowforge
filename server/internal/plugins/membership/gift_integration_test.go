package membership_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugins/membership"
)

// 礼品卡：购买 → 生成属于购买者的兑换码 → 送给他人兑换并通知购买者；退款撤销时未兑换的按比例缩短/作废，已兑换的从兑换者扣回。
func TestGiftCards(t *testing.T) {
	e := newTestEnv(t)
	buyer, friend := e.user(t, "giver"), e.user(t, "friend")
	e.setPlugin(t, "payment", true)
	e.do(t, http.MethodPut, "/api/v1/admin/payment/settings", `{"offline_enabled":true,"offline_instructions":"转账"}`)
	plan := e.createPlan(t, `{"name":"年卡","entitlements":{"books.max":20},"prices":[{"duration_days":360,"price_cents":9000}]}`)
	var price membership.Price
	e.db.Where("plan_id = ?", plan).First(&price)

	notices := func(u *models.User, text string) int64 {
		var n int64
		e.db.Model(&models.Notification{}).Where("user_id = ? AND title LIKE ?", u.ID, "%"+text+"%").Count(&n)
		return n
	}
	buy := func() string {
		t.Helper()
		_, prod := e.doAs(t, buyer, http.MethodGet, fmt.Sprintf("/api/v1/payment/products/membership_gift/%d", price.ID), "")
		p := data(prod)["product"].(map[string]any)
		if p["title"] != "年卡（礼品卡）" || p["amount_cents"].(float64) != 9000 || p["duration_days"].(float64) != 360 {
			t.Fatalf("礼品卡商品解析错误: %v", p)
		}
		status, created := e.doAs(t, buyer, http.MethodPost, "/api/v1/payment/orders", fmt.Sprintf(`{"kind":"membership_gift","sku":"%d","channel":"offline"}`, price.ID))
		if status != http.StatusOK {
			t.Fatalf("下单失败: %d %v", status, created)
		}
		no := data(created)["order"].(map[string]any)["order_no"].(string)
		if status, p := e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no+"/confirm", ""); status != http.StatusOK {
			t.Fatalf("确认收款失败: %d %v", status, p)
		}
		return no
	}
	gifts := func() []map[string]any {
		_, p := e.doAs(t, buyer, http.MethodGet, "/api/v1/users/me/membership/gifts", "")
		out := []map[string]any{}
		for _, it := range data(p)["items"].([]any) {
			out = append(out, it.(map[string]any))
		}
		return out
	}

	// 购买后生成礼品卡：购买者自己不会开通会员，重复确认不重复生成
	no := buy()
	e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no+"/confirm", "")
	list := gifts()
	if len(list) != 1 || list[0]["status"] != "unused" || list[0]["plan_name"] != "年卡" || list[0]["days"].(float64) != 360 || len(list[0]["code"].(string)) != 19 || list[0]["order_no"] != no {
		t.Fatalf("礼品卡异常: %v", list)
	}
	var n int64
	e.db.Model(&membership.UserMembership{}).Where("user_id = ?", buyer.ID).Count(&n)
	if n != 0 || notices(buyer, "礼品卡已生成") != 1 {
		t.Fatalf("购买礼品卡不应给购买者开通会员，并应通知: %d", n)
	}
	// 礼品卡不出现在后台兑换码批次中；别人看不到
	if _, p := e.do(t, http.MethodGet, "/api/v1/admin/membership/redeem/batches", ""); data(p)["total"].(float64) != 0 {
		t.Fatalf("礼品卡不应出现在兑换码批次中: %v", p)
	}
	if _, p := e.doAs(t, friend, http.MethodGet, "/api/v1/users/me/membership/gifts", ""); data(p)["total"].(float64) != 0 {
		t.Fatalf("只能看到自己购买的礼品卡: %v", p)
	}

	// 送给朋友兑换：朋友开通，购买者收到通知
	code := list[0]["code"].(string)
	if status, p := e.doAs(t, friend, http.MethodPost, "/api/v1/membership/redeem", fmt.Sprintf(`{"code":%q}`, code)); status != http.StatusOK || data(p)["action"] != "grant" {
		t.Fatalf("兑换礼品卡失败: %d %v", status, p)
	}
	near(t, e.membership(t, friend.ID).ExpiresAt, time.Now().Add(360*24*time.Hour), "兑换礼品卡后到期时间")
	if list := gifts(); list[0]["status"] != "redeemed" || list[0]["redeemed_by_me"] != false || list[0]["redeemed_at"] == nil {
		t.Fatalf("兑换后状态异常: %v", list)
	}
	if notices(buyer, "礼品卡已被兑换") != 1 {
		t.Fatal("礼品卡被兑换后应通知购买者")
	}
	// 已兑换的礼品卡退款撤销：按比例从兑换者扣回
	if status, r := e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no+"/refunds", `{"amount_cents":4500,"reason":"部分退款","revoke":true}`); status != http.StatusOK || data(r)["status"] != "succeeded" {
		t.Fatalf("退款失败: %d %v", status, r)
	}
	near(t, e.membership(t, friend.ID).ExpiresAt, time.Now().Add(180*24*time.Hour), "退款后兑换者到期时间（扣回 180 天）")
	if list := gifts(); list[0]["days"].(float64) != 180 {
		t.Fatalf("退款后礼品卡天数应相应减少: %v", list[0])
	}
	// 再退 1/4：仍按原始 360 天折算扣回 90 天
	e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no+"/refunds", `{"amount_cents":2250,"reason":"再退一部分","revoke":true}`)
	near(t, e.membership(t, friend.ID).ExpiresAt, time.Now().Add(90*24*time.Hour), "再次退款后兑换者到期时间")

	// 未兑换的礼品卡：部分退款缩短天数，退完作废，作废后不能兑换
	no2 := buy()
	e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no2+"/refunds", `{"amount_cents":3000,"reason":"部分退款","revoke":true}`)
	list = gifts()
	if list[0]["status"] != "unused" || list[0]["days"].(float64) != 240 {
		t.Fatalf("部分退款后应缩短天数: %v", list[0])
	}
	code2 := list[0]["code"].(string)
	e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no2+"/refunds", `{"amount_cents":6000,"reason":"全部退款","revoke":true}`)
	list = gifts()
	if list[0]["status"] != "void" || list[0]["code"] != "" {
		t.Fatalf("退完后应作废且不再显示兑换码: %v", list[0])
	}
	if status, _ := e.doAs(t, buyer, http.MethodPost, "/api/v1/membership/redeem", fmt.Sprintf(`{"code":%q}`, code2)); status != http.StatusBadRequest {
		t.Fatalf("作废的礼品卡不能兑换: %d", status)
	}

	// 不撤销的退款不影响礼品卡；购买者也可以自己兑换
	no3 := buy()
	e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no3+"/refunds", `{"amount_cents":1000,"reason":"补偿","revoke":false}`)
	list = gifts()
	if list[0]["days"].(float64) != 360 {
		t.Fatalf("不撤销的退款不应影响礼品卡: %v", list[0])
	}
	if status, _ := e.doAs(t, buyer, http.MethodPost, "/api/v1/membership/redeem", fmt.Sprintf(`{"code":%q}`, list[0]["code"])); status != http.StatusOK {
		t.Fatalf("购买者可以自己兑换: %d", status)
	}
	if list := gifts(); list[0]["status"] != "redeemed" || list[0]["redeemed_by_me"] != true || notices(buyer, "礼品卡已被兑换") != 1 {
		t.Fatalf("自己兑换不应通知自己: %v", list[0])
	}
}
