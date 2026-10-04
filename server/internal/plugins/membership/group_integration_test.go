package membership_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugins/membership"
	"knowforge/server/internal/plugins/teams"
)

// 团队会员：团队所有者 / 管理员按席位购买（线下转账确认后开通），席位按团队的成员次序覆盖；与个人会员逐项取较高值；
// 有效期内只能按原方案与席位续期；增加席位按剩余天数折算；退款撤销扣回席位；管理员调整与取消；到期提醒购买人。
func TestGroupMembership(t *testing.T) {
	e := newTestEnv(t)
	e.setPlugin(t, "payment", true)
	e.setPlugin(t, "teams", true)
	e.do(t, http.MethodPut, "/api/v1/admin/payment/settings", `{"offline_enabled":true,"offline_instructions":"转账"}`)
	teamPlan := e.createPlan(t, `{"name":"团队版","group_enabled":true,"entitlements":{"books.max":30,"teams.members":50},"prices":[{"duration_days":30,"price_cents":1000}]}`)
	personalPlan := e.createPlan(t, `{"name":"个人版","entitlements":{"books.max":40},"prices":[{"duration_days":30,"price_cents":500}]}`)
	var price, personalPrice membership.Price
	e.db.Where("plan_id = ?", teamPlan).First(&price)
	e.db.Where("plan_id = ?", personalPlan).First(&personalPrice)

	owner, admin, m1, m2 := e.user(t, "g-owner"), e.user(t, "g-admin"), e.user(t, "g-m1"), e.user(t, "g-m2")
	_, created := e.doAs(t, owner, http.MethodPost, "/api/v1/teams", `{"name":"Alpha"}`)
	teamID := uint(data(created)["id"].(float64))
	base := time.Now()
	for i, m := range []struct {
		u    *models.User
		role string
	}{{m1, teams.RoleMember}, {admin, teams.RoleAdmin}, {m2, teams.RoleMember}} {
		e.db.Create(&teams.Member{TeamID: teamID, UserID: m.u.ID, Role: m.role, Status: "accepted", CreatedAt: base.Add(time.Duration(i+1) * time.Second)})
	}

	buy := func(u *models.User, kind, sku string) string {
		t.Helper()
		status, order := e.doAs(t, u, http.MethodPost, "/api/v1/payment/orders", fmt.Sprintf(`{"kind":%q,"sku":%q,"channel":"offline"}`, kind, sku))
		if status != http.StatusOK {
			t.Fatalf("下单失败: %d %v", status, order)
		}
		no := data(order)["order"].(map[string]any)["order_no"].(string)
		if status, p := e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no+"/confirm", ""); status != http.StatusOK {
			t.Fatalf("确认收款失败: %d %v", status, p)
		}
		return no
	}
	sku := func(seats int) string { return fmt.Sprintf("%d-team-%d-%d", price.ID, teamID, seats) }

	// 商品：按席位计价；普通成员不能购买
	_, prod := e.doAs(t, owner, http.MethodGet, "/api/v1/payment/products/membership_group/"+sku(2), "")
	if p := data(prod)["product"].(map[string]any); p["amount_cents"].(float64) != 2000 || !strings.Contains(p["title"].(string), "Alpha") {
		t.Fatalf("团队会员商品不对: %v", p)
	}
	if status, _ := e.doAs(t, m1, http.MethodGet, "/api/v1/payment/products/membership_group/"+sku(2), ""); status != http.StatusBadRequest {
		t.Fatalf("普通成员不能购买团队会员: %d", status)
	}
	if status, _ := e.doAs(t, owner, http.MethodGet, fmt.Sprintf("/api/v1/payment/products/membership_group/%d-team-%d-2", personalPrice.ID, teamID), ""); status != http.StatusBadRequest {
		t.Fatalf("未开启团队购买的方案不能按团队购买: %d", status)
	}
	buy(owner, "membership_group", sku(2))

	// 席位覆盖：所有者、管理员优先，其次按加入时间
	if v, s := e.entitlements(t, admin); v["books.max"] != 30 || s["books.max"] != "membership" {
		t.Fatalf("管理员应在席位内: %v %v", v, s)
	}
	if v, s := e.entitlements(t, m1); s["books.max"] == "membership" {
		t.Fatalf("席位已满，成员不应享有团队会员: %v %v", v, s)
	}
	_, mine := e.doAs(t, m1, http.MethodGet, "/api/v1/users/me/membership", "")
	if g := data(mine)["groups"].([]any); len(g) != 1 || g[0].(map[string]any)["covered"] != false {
		t.Fatalf("我的会员应显示团队会员但不在席位内: %v", g)
	}

	// 有效期内只能按原方案与席位续期
	if status, _ := e.doAs(t, owner, http.MethodGet, "/api/v1/payment/products/membership_group/"+sku(3), ""); status != http.StatusBadRequest {
		t.Fatalf("有效期内不能直接改席位: %d", status)
	}

	// 增加席位：按剩余天数折算（约 30 天 × 1000/30 分）
	_, sp := e.doAs(t, admin, http.MethodGet, fmt.Sprintf("/api/v1/payment/products/membership_group_seats/team-%d-1", teamID), "")
	seatsAmount := data(sp)["product"].(map[string]any)["amount_cents"].(float64)
	if amt := seatsAmount; amt < 990 || amt > 1010 {
		t.Fatalf("增加席位的价格应按剩余天数折算: %v", amt)
	}
	seatsOrder := buy(admin, "membership_group_seats", fmt.Sprintf("team-%d-1", teamID))
	if v, _ := e.entitlements(t, m1); v["books.max"] != 30 {
		t.Fatalf("增加席位后成员应享有团队会员: %v", v)
	}

	// 个人会员与团队会员逐项取较高值
	buy(admin, "membership", fmt.Sprint(personalPrice.ID))
	if v, _ := e.entitlements(t, admin); v["books.max"] != 40 || v["teams.members"] != 50 {
		t.Fatalf("个人与团队会员应逐项取较高值: %v", v)
	}

	// 团队会员详情：成员与覆盖情况
	_, detail := e.doAs(t, owner, http.MethodGet, fmt.Sprintf("/api/v1/membership/groups/team/%d", teamID), "")
	covered := 0
	for _, m := range data(detail)["members"].([]any) {
		if m.(map[string]any)["covered"] == true {
			covered++
		}
	}
	if covered != 3 || data(detail)["subscription"].(map[string]any)["seats"].(float64) != 3 {
		t.Fatalf("团队会员详情不对: %v", data(detail))
	}
	if status, _ := e.doAs(t, m2, http.MethodGet, fmt.Sprintf("/api/v1/membership/groups/team/%d", teamID), ""); status != http.StatusNotFound {
		t.Fatalf("普通成员不能管理团队会员: %d", status)
	}
	_, groups := e.doAs(t, owner, http.MethodGet, "/api/v1/membership/groups", "")
	if items := data(groups)["items"].([]any); len(items) != 1 || len(data(groups)["plans"].([]any)) != 1 {
		t.Fatalf("我管理的团队与可选方案不对: %v", data(groups))
	}

	// 退款并撤销增加的席位：扣回席位
	if status, r := e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+seatsOrder+"/refunds", fmt.Sprintf(`{"amount_cents":%v,"reason":"退席位","revoke":true}`, seatsAmount)); status != http.StatusOK {
		t.Fatalf("退款失败: %d %v", status, r)
	}
	var sub membership.GroupMembership
	e.db.Where("group_kind = ? AND group_id = ?", "team", teamID).First(&sub)
	if sub.Seats != 2 {
		t.Fatalf("退款撤销后应扣回席位: %d", sub.Seats)
	}

	// 到期提醒购买人
	e.db.Model(&membership.GroupMembership{}).Where("id = ?", sub.ID).Update("expires_at", time.Now().Add(48*time.Hour))
	membership.Sweep(e.app)
	var n int64
	e.db.Model(&models.Notification{}).Where("user_id = ? AND payload LIKE ?", owner.ID, "%membership/teams%").Count(&n)
	if n < 2 { // 开通通知 + 到期提醒
		t.Fatalf("到期前应提醒购买人: %d", n)
	}

	// 管理员调整与取消
	if status, p := e.do(t, http.MethodPut, fmt.Sprintf("/api/v1/admin/membership/groups/team/%d", teamID),
		fmt.Sprintf(`{"plan_id":%d,"seats":4,"expires_at":%q}`, teamPlan, time.Now().Add(90*24*time.Hour).Format(time.RFC3339))); status != http.StatusOK {
		t.Fatalf("管理员调整失败: %d %v", status, p)
	}
	if v, _ := e.entitlements(t, m2); v["books.max"] != 30 {
		t.Fatalf("调整为 4 席后所有成员都应覆盖: %v", v)
	}
	e.do(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/membership/groups/team/%d/revoke", teamID), "")
	if _, s := e.entitlements(t, m2); s["books.max"] == "membership" {
		t.Fatalf("取消后团队会员应失效: %v", s)
	}
	_, list := e.do(t, http.MethodGet, "/api/v1/admin/membership/groups", "")
	if data(list)["total"].(float64) != 1 {
		t.Fatalf("管理端应列出团队会员: %v", data(list))
	}
}
