package membership_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugins/membership"
)

// 邀请奖励：设置校验、邀请注册（验证邮箱）奖励、首次购买双方奖励、只奖励首次、每月上限、奖励加在当前方案上、退款扣回。
func TestReferralRewards(t *testing.T) {
	e := newTestEnv(t)
	e.setPlugin(t, "payment", true)
	e.do(t, http.MethodPut, "/api/v1/admin/payment/settings", `{"offline_enabled":true,"offline_instructions":"转账"}`)
	pro := e.createPlan(t, `{"name":"专业版","prices":[{"duration_days":30,"price_cents":3000}]}`)
	basic := e.createPlan(t, `{"name":"基础版","prices":[{"duration_days":30,"price_cents":1000}]}`)
	var proPrice membership.Price
	e.db.Where("plan_id = ?", pro).First(&proPrice)

	inviter := e.user(t, "alice")
	e.db.Model(inviter).Updates(map[string]any{"invite_code": "ALICE1", "invite_code_enabled": true})
	register := func(name string) *models.User {
		t.Helper()
		status, p := e.request(t, "", http.MethodPost, "/api/v1/auth/register", fmt.Sprintf(`{"username":%q,"email":"%s@test.local","password":"Secret123!","invite_code":"alice1"}`, name, name))
		if status != http.StatusOK {
			t.Fatalf("注册失败: %d %v", status, p)
		}
		var u models.User
		e.db.Where("username = ?", name).First(&u)
		if u.InvitedBy != inviter.ID {
			t.Fatalf("应记录邀请人: %+v", u)
		}
		return &u
	}
	buy := func(u *models.User) string {
		t.Helper()
		_, created := e.doAs(t, u, http.MethodPost, "/api/v1/payment/orders", fmt.Sprintf(`{"kind":"membership","sku":"%d","channel":"offline"}`, proPrice.ID))
		no := data(created)["order"].(map[string]any)["order_no"].(string)
		if status, p := e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no+"/confirm", ""); status != http.StatusOK {
			t.Fatalf("确认收款失败: %d %v", status, p)
		}
		return no
	}
	notices := func(u *models.User, text string) int64 {
		var n int64
		e.db.Model(&models.Notification{}).Where("user_id = ? AND title LIKE ?", u.ID, "%"+text+"%").Count(&n)
		return n
	}

	// 未开启时不奖励
	early := register("early")
	var n int64
	e.db.Model(&membership.UserMembership{}).Where("user_id = ?", inviter.ID).Count(&n)
	if n != 0 {
		t.Fatal("未开启时不应奖励")
	}
	// 设置校验
	if status, _ := e.do(t, http.MethodPut, "/api/v1/admin/membership/referral", `{"enabled":true}`); status != http.StatusBadRequest {
		t.Fatalf("开启时必须选择方案: %d", status)
	}
	if status, _ := e.do(t, http.MethodPut, "/api/v1/admin/membership/referral", fmt.Sprintf(`{"enabled":true,"plan_id":%d,"inviter_days":400}`, pro)); status != http.StatusBadRequest {
		t.Fatalf("奖励天数应被校验: %d", status)
	}
	status, p := e.do(t, http.MethodPut, "/api/v1/admin/membership/referral", fmt.Sprintf(`{"enabled":true,"plan_id":%d,"inviter_days":30,"invitee_days":7,"signup_days":3,"monthly_limit":2}`, pro))
	if status != http.StatusOK || data(p)["settings"].(map[string]any)["inviter_days"].(float64) != 30 {
		t.Fatalf("保存设置失败: %d %v", status, p)
	}

	// 被邀请人注册（无需激活即视为已验证邮箱）：邀请人获得 3 天
	bob := register("bob")
	m := e.membership(t, inviter.ID)
	near(t, m.ExpiresAt, time.Now().Add(3*24*time.Hour), "注册奖励后邀请人到期时间")
	if notices(inviter, "邀请奖励") != 1 {
		t.Fatal("邀请人应收到奖励通知")
	}
	// 首次购买：被邀请人 30+7 天，邀请人再得 30 天
	no := buy(bob)
	near(t, e.membership(t, bob.ID).ExpiresAt, time.Now().Add(37*24*time.Hour), "被邀请人首次购买后到期时间")
	near(t, e.membership(t, inviter.ID).ExpiresAt, time.Now().Add(33*24*time.Hour), "首次购买奖励后邀请人到期时间")
	if notices(bob, "受邀开通会员") != 1 {
		t.Fatal("被邀请人应收到额外奖励通知")
	}
	// 再次购买不再奖励
	buy(bob)
	near(t, e.membership(t, inviter.ID).ExpiresAt, time.Now().Add(33*24*time.Hour), "再次购买不应奖励邀请人")
	// 未开启时注册的用户之后首次购买仍可奖励，但本月已达上限（2 次）：记录但邀请人不获奖励，被邀请人照常
	buy(early)
	near(t, e.membership(t, early.ID).ExpiresAt, time.Now().Add(37*24*time.Hour), "达到上限时被邀请人仍获奖励")
	near(t, e.membership(t, inviter.ID).ExpiresAt, time.Now().Add(33*24*time.Hour), "达到上限后邀请人不再获奖励")
	_, mine := e.doAs(t, inviter, http.MethodGet, "/api/v1/users/me/membership/referral", "")
	if d := data(mine); d["enabled"] != true || d["total_days"].(float64) != 33 || len(d["rewards"].([]any)) != 3 || d["rules"].(map[string]any)["plan_name"] != "专业版" {
		t.Fatalf("我的邀请奖励异常: %v", d)
	}

	// 触发奖励的订单全额退款并撤销：被邀请人扣回订单 30 天与奖励 7 天，邀请人扣回 30 天
	e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no+"/refunds", `{"amount_cents":3000,"reason":"退款","revoke":true}`)
	near(t, e.membership(t, bob.ID).ExpiresAt, time.Now().Add(30*24*time.Hour), "退款后被邀请人到期时间（第二笔订单仍在）")
	near(t, e.membership(t, inviter.ID).ExpiresAt, time.Now().Add(3*24*time.Hour), "退款后邀请人到期时间")

	// 已有其他方案的邀请人：奖励加在当前方案上，不更换方案
	carolInviter := e.user(t, "carol")
	e.db.Model(carolInviter).Updates(map[string]any{"invite_code": "CAROL1", "invite_code_enabled": true})
	e.do(t, http.MethodPost, "/api/v1/admin/membership/grant", fmt.Sprintf(`{"user_id":%d,"plan_id":%d,"days":10}`, carolInviter.ID, basic))
	e.request(t, "", http.MethodPost, "/api/v1/auth/register", `{"username":"dave","email":"dave@test.local","password":"Secret123!","invite_code":"CAROL1"}`)
	cm := e.membership(t, carolInviter.ID)
	if cm.PlanID != basic {
		t.Fatalf("奖励不应更换邀请人的方案: %+v", cm)
	}
	near(t, cm.ExpiresAt, time.Now().Add(13*24*time.Hour), "奖励加在当前方案上")

	// 后台可查看奖励记录
	_, admin := e.do(t, http.MethodGet, "/api/v1/admin/membership/referral", "")
	if data(admin)["total"].(float64) != 4 {
		t.Fatalf("后台奖励记录异常: %v", data(admin))
	}
}
