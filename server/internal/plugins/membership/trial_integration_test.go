package membership_test

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugins/membership"
)

// 免费试用：方案试用天数校验、邮箱验证要求、每人一次（并发）、开过会员的不能试用、试用中购买顺延并转正、
// 试用不影响新用户优惠券、试用的到期提醒文案。
func TestTrials(t *testing.T) {
	e := newTestEnv(t)
	if status, _ := e.do(t, http.MethodPost, "/api/v1/admin/membership/plans", `{"name":"超长试用","trial_days":400,"prices":[]}`); status != http.StatusBadRequest {
		t.Fatalf("试用天数应被校验: %d", status)
	}
	pro := e.createPlan(t, `{"name":"专业版","trial_days":7,"prices":[{"duration_days":30,"price_cents":1900}]}`)
	noTrial := e.createPlan(t, `{"name":"基础版","prices":[{"duration_days":30,"price_cents":900}]}`)
	trialURL := func(plan uint) string { return fmt.Sprintf("/api/v1/membership/plans/%d/trial", plan) }
	trialInfo := func(u *models.User) map[string]any {
		_, p := e.doAs(t, u, http.MethodGet, "/api/v1/users/me/membership", "")
		return data(p)["trial"].(map[string]any)
	}
	notices := func(u *models.User, text string) int64 {
		var n int64
		e.db.Model(&models.Notification{}).Where("user_id = ? AND title LIKE ?", u.ID, "%"+text+"%").Count(&n)
		return n
	}
	if _, p := e.doAs(t, e.user(t, "viewer"), http.MethodGet, "/api/v1/membership/plans", ""); data(p)["items"].([]any)[0].(map[string]any)["trial_days"].(float64) != 7 {
		t.Fatalf("公开方案应包含试用天数: %v", p)
	}

	// 默认需验证邮箱
	unverified := &models.User{Username: "newbie", Email: "newbie@test.local", Role: "user", IsActive: true}
	e.db.Create(unverified)
	if info := trialInfo(unverified); info["eligible"] != false || info["needs_verified_email"] != true {
		t.Fatalf("未验证邮箱不能试用: %v", info)
	}
	if status, _ := e.doAs(t, unverified, http.MethodPost, trialURL(pro), ""); status != http.StatusBadRequest {
		t.Fatalf("未验证邮箱应被拒绝: %d", status)
	}
	e.do(t, http.MethodPut, "/api/v1/admin/membership/settings", `{"trial_verified_email":false}`)
	if info := trialInfo(unverified); info["eligible"] != true {
		t.Fatalf("关闭邮箱要求后应可试用: %v", info)
	}
	e.do(t, http.MethodPut, "/api/v1/admin/membership/settings", `{"trial_verified_email":true}`)

	// 领取：并发只成功一次；不提供试用的方案不能领
	alice := e.user(t, "alice")
	if status, _ := e.doAs(t, alice, http.MethodPost, trialURL(noTrial), ""); status != http.StatusBadRequest {
		t.Fatalf("不提供试用的方案不能领取: %d", status)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	okCount := 0
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if status, _ := e.doAs(t, alice, http.MethodPost, trialURL(pro), ""); status == http.StatusOK {
				mu.Lock()
				okCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if okCount != 1 {
		t.Fatalf("试用只能领取一次，实际成功 %d 次", okCount)
	}
	m := e.membership(t, alice.ID)
	near(t, m.ExpiresAt, time.Now().Add(7*24*time.Hour), "试用到期时间")
	if !m.Trial || notices(alice, "开始「专业版」免费试用") != 1 || trialInfo(alice)["eligible"] != false {
		t.Fatalf("应处于试用中并通知: %+v", m)
	}
	var rec membership.Record
	e.db.Where("user_id = ?", alice.ID).First(&rec)
	if rec.Source != "trial" || rec.Days != 7 {
		t.Fatalf("试用流水异常: %+v", rec)
	}

	// 开过会员的用户不能试用
	bob := e.user(t, "bob")
	e.do(t, http.MethodPost, "/api/v1/admin/membership/grant", fmt.Sprintf(`{"user_id":%d,"plan_id":%d,"days":1}`, bob.ID, noTrial))
	if status, _ := e.doAs(t, bob, http.MethodPost, trialURL(pro), ""); status != http.StatusBadRequest || trialInfo(bob)["eligible"] != false {
		t.Fatalf("开过会员的用户不能试用: %d", status)
	}

	// 试用中的到期提醒使用试用文案
	e.db.Model(&membership.UserMembership{}).Where("user_id = ?", alice.ID).Update("expires_at", time.Now().Add(24*time.Hour))
	membership.Sweep(e.app)
	if notices(alice, "试用将于") != 1 || notices(alice, "会员将于") != 0 {
		t.Fatal("试用到期前应发送试用提醒")
	}

	// 试用不影响新用户优惠券；试用中购买同一方案：在剩余试用期后顺延并转正
	e.setPlugin(t, "payment", true)
	e.do(t, http.MethodPut, "/api/v1/admin/payment/settings", `{"offline_enabled":true,"offline_instructions":"转账"}`)
	e.do(t, http.MethodPost, "/api/v1/admin/membership/coupons", `{"name":"新人券","code":"WELCOME","type":"percent","percent_off":50,"new_members_only":true}`)
	var price membership.Price
	e.db.Where("plan_id = ?", pro).First(&price)
	status, created := e.doAs(t, alice, http.MethodPost, "/api/v1/payment/orders", fmt.Sprintf(`{"kind":"membership","sku":"%d","channel":"offline","coupon":"WELCOME"}`, price.ID))
	if status != http.StatusOK || data(created)["order"].(map[string]any)["amount_cents"].(float64) != 950 {
		t.Fatalf("试用用户应可使用新用户优惠券: %d %v", status, created)
	}
	no := data(created)["order"].(map[string]any)["order_no"].(string)
	e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no+"/confirm", "")
	m = e.membership(t, alice.ID)
	near(t, m.ExpiresAt, time.Now().Add(31*24*time.Hour), "试用中购买后的到期时间")
	if m.Trial {
		t.Fatal("购买后不应再是试用")
	}
}
