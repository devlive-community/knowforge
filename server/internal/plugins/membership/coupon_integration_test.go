package membership_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins/membership"
)

// 优惠券：校验、结算试算、下单占用与支付确认、取消与过期归还、总次数与每人次数、首次开通限制、适用方案与礼品卡、停用。
func TestCoupons(t *testing.T) {
	e := newTestEnv(t)
	e.setPlugin(t, "payment", true)
	e.do(t, http.MethodPut, "/api/v1/admin/payment/settings", `{"offline_enabled":true,"offline_instructions":"转账"}`)
	pro := e.createPlan(t, `{"name":"专业版","prices":[{"duration_days":360,"price_cents":9000}]}`)
	basic := e.createPlan(t, `{"name":"基础版","prices":[{"duration_days":30,"price_cents":1000}]}`)
	priceOf := func(plan uint) uint {
		var p membership.Price
		e.db.Where("plan_id = ?", plan).First(&p)
		return p.ID
	}
	proPrice, basicPrice := priceOf(pro), priceOf(basic)
	alice, bob, carol := e.user(t, "alice"), e.user(t, "bob"), e.user(t, "carol")

	product := func(u *models.User, kind string, price uint, coupon string) (int, map[string]any) {
		return e.doAs(t, u, http.MethodGet, fmt.Sprintf("/api/v1/payment/products/%s/%d?coupon=%s", kind, price, coupon), "")
	}
	order := func(u *models.User, kind string, price uint, coupon string) (int, map[string]any) {
		status, p := e.doAs(t, u, http.MethodPost, "/api/v1/payment/orders", fmt.Sprintf(`{"kind":%q,"sku":"%d","channel":"offline","coupon":%q}`, kind, price, coupon))
		if status != http.StatusOK {
			return status, p
		}
		return status, data(p)["order"].(map[string]any)
	}
	coupon := func(code string) map[string]any {
		_, p := e.do(t, http.MethodGet, "/api/v1/admin/membership/coupons", "")
		for _, it := range data(p)["items"].([]any) {
			if m := it.(map[string]any); m["code"] == code {
				return m
			}
		}
		t.Fatalf("找不到优惠码 %s", code)
		return nil
	}

	// 没有优惠券时结算页不显示优惠码
	if _, p := product(alice, "membership", proPrice, ""); data(p)["coupon_supported"] != false {
		t.Fatalf("没有优惠券时不应支持优惠码: %v", p)
	}
	// 校验
	for _, body := range []string{
		`{"name":"","type":"percent","percent_off":20}`,
		`{"name":"x","type":"percent","percent_off":100}`,
		`{"name":"x","type":"amount","amount_off_cents":1000,"min_amount_cents":1000}`,
		`{"name":"x","type":"percent","percent_off":20,"code":"a!"}`,
		`{"name":"x","type":"percent","percent_off":20,"plan_ids":[999]}`,
	} {
		if status, _ := e.do(t, http.MethodPost, "/api/v1/admin/membership/coupons", body); status != http.StatusBadRequest {
			t.Fatalf("应拒绝: %s -> %d", body, status)
		}
	}

	// 新用户 8 折：仅专业版、仅首次开通、共 2 次、每人 1 次
	status, p := e.do(t, http.MethodPost, "/api/v1/admin/membership/coupons", fmt.Sprintf(`{"name":"新用户 8 折","code":"new-20","type":"percent","percent_off":20,"plan_ids":[%d],"new_members_only":true,"max_uses":2}`, pro))
	if status != http.StatusOK || data(p)["code"] != "NEW20" || data(p)["per_user_limit"].(float64) != 1 {
		t.Fatalf("创建优惠券失败: %d %v", status, p)
	}
	if status, _ := e.do(t, http.MethodPost, "/api/v1/admin/membership/coupons", `{"name":"重复","code":"NEW20","type":"percent","percent_off":5}`); status != http.StatusBadRequest {
		t.Fatalf("优惠码不能重复: %d", status)
	}
	// 试算：不占用次数
	_, p = product(alice, "membership", proPrice, "new20")
	if d := data(p)["discount"].(map[string]any); data(p)["coupon_supported"] != true || d["amount_off_cents"].(float64) != 1800 || d["label"] != "新用户 8 折" {
		t.Fatalf("试算异常: %v", p)
	}
	if status, p := product(alice, "membership", basicPrice, "new20"); status != http.StatusBadRequest || p["message"] == "" {
		t.Fatalf("不适用的方案应被拒绝: %d %v", status, p)
	}
	if _, p := product(alice, "membership_gift", proPrice, ""); data(p)["coupon_supported"] != false {
		t.Fatalf("不含礼品卡时礼品卡不应显示优惠码: %v", p)
	}
	if status, _ := product(alice, "membership", proPrice, "NOPE"); status != http.StatusBadRequest {
		t.Fatalf("无效优惠码应被拒绝: %d", status)
	}

	// 下单占用：实付 72 元；同一人不能再次使用；取消后归还
	status, o := order(alice, "membership", proPrice, "new20")
	if status != http.StatusOK || o["amount_cents"].(float64) != 7200 || o["original_cents"].(float64) != 9000 || o["discount_cents"].(float64) != 1800 || o["coupon_code"] != "NEW20" {
		t.Fatalf("使用优惠码下单异常: %d %v", status, o)
	}
	if c := coupon("NEW20"); c["reserved"].(float64) != 1 || c["used"].(float64) != 0 {
		t.Fatalf("下单后应占用一次: %v", c)
	}
	if status, _ := order(alice, "membership", proPrice, "NEW20"); status != http.StatusBadRequest {
		t.Fatalf("每人限用一次: %d", status)
	}
	e.doAs(t, alice, http.MethodPost, fmt.Sprintf("/api/v1/payment/orders/%s/cancel", o["order_no"]), "")
	if c := coupon("NEW20"); c["reserved"].(float64) != 0 {
		t.Fatalf("取消订单后应归还: %v", c)
	}
	// 重新下单并支付：确认使用，按优惠价开通
	_, o = order(alice, "membership", proPrice, "NEW20")
	if status, p := e.do(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/payment/orders/%s/confirm", o["order_no"]), ""); status != http.StatusOK {
		t.Fatalf("确认收款失败: %d %v", status, p)
	}
	if c := coupon("NEW20"); c["used"].(float64) != 1 || c["reserved"].(float64) != 0 {
		t.Fatalf("支付后应确认使用: %v", c)
	}
	near(t, e.membership(t, alice.ID).ExpiresAt, time.Now().Add(360*24*time.Hour), "优惠价开通的到期时间")

	// 总次数：bob 占用最后一次，carol 用不了；bob 的订单过期后归还，carol 可用
	_, bobOrder := order(bob, "membership", proPrice, "NEW20")
	if status, _ := order(carol, "membership", proPrice, "NEW20"); status != http.StatusBadRequest {
		t.Fatalf("次数用完后应被拒绝: %d", status)
	}
	e.db.Table("payment_orders").Where("order_no = ?", bobOrder["order_no"]).Update("expires_at", time.Now().Add(-time.Minute))
	plugincore.FireJobQueueSweep(e.app, e.app.Jobs)
	if status, o := order(carol, "membership", proPrice, "NEW20"); status != http.StatusOK || o["amount_cents"].(float64) != 7200 {
		t.Fatalf("过期订单归还后应可使用: %d %v", status, o)
	}
	// 仅限首次开通：已开通过会员的 alice 不能用新用户券
	e.do(t, http.MethodPost, "/api/v1/admin/membership/coupons", `{"name":"新人专享","code":"FIRST","type":"percent","percent_off":10}`)
	e.do(t, http.MethodPost, "/api/v1/admin/membership/coupons", `{"name":"新人专享2","code":"FIRST2","type":"percent","percent_off":10,"new_members_only":true}`)
	if status, _ := product(alice, "membership", basicPrice, "FIRST"); status != http.StatusOK {
		t.Fatalf("不限新用户的优惠码应可用: %d", status)
	}
	if status, p := product(alice, "membership", basicPrice, "FIRST2"); status != http.StatusBadRequest {
		t.Fatalf("已开通过会员的用户不能用新用户券: %d %v", status, p)
	}

	// 满减并可用于礼品卡：满 50 减 10；基础版 10 元不满足
	e.do(t, http.MethodPost, "/api/v1/admin/membership/coupons", `{"name":"满 50 减 10","code":"GIFT10","type":"amount","amount_off_cents":1000,"min_amount_cents":5000,"include_gifts":true,"per_user_limit":0}`)
	if _, p := product(bob, "membership_gift", proPrice, "GIFT10"); data(p)["discount"].(map[string]any)["amount_off_cents"].(float64) != 1000 {
		t.Fatalf("礼品卡满减异常: %v", p)
	}
	if status, _ := product(bob, "membership_gift", basicPrice, "GIFT10"); status != http.StatusBadRequest {
		t.Fatalf("未满最低消费应被拒绝: %d", status)
	}
	// 不限每人次数：可多次使用
	for i := 0; i < 2; i++ {
		if status, o := order(bob, "membership_gift", proPrice, "GIFT10"); status != http.StatusOK || o["amount_cents"].(float64) != 8000 {
			t.Fatalf("第 %d 次使用失败: %d %v", i+1, status, o)
		}
	}

	// 停用后不可用；使用记录可查
	id := uint(coupon("GIFT10")["id"].(float64))
	if status, p := e.do(t, http.MethodPut, fmt.Sprintf("/api/v1/admin/membership/coupons/%d", id), `{"status":"disabled"}`); status != http.StatusOK || data(p)["status"] != "disabled" {
		t.Fatalf("停用失败: %d %v", status, p)
	}
	if status, _ := product(bob, "membership_gift", proPrice, "GIFT10"); status != http.StatusBadRequest {
		t.Fatalf("停用后不可用: %d", status)
	}
	_, uses := e.do(t, http.MethodGet, fmt.Sprintf("/api/v1/admin/membership/coupons/%d/uses", id), "")
	items := data(uses)["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["user"].(map[string]any)["username"] != "bob" {
		t.Fatalf("使用记录异常: %v", items)
	}
}
