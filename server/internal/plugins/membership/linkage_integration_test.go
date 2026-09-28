package membership_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// 会员与成长、成就联动：付款开通会员得「购买会员」经验；会员方案的经验加成放大按规则获得的经验（每日上限按基础经验计）；
// 会员指标驱动成就（首次成为会员）；退款撤销后收回该订单的经验；管理员不会因「不受限制」获得最大加成。
func TestMembershipGrowthAndAchievements(t *testing.T) {
	e := newTestEnv(t)
	for _, key := range []string{"payment", "growth", "achievements"} {
		e.setPlugin(t, key, true)
	}
	e.do(t, http.MethodPut, "/api/v1/admin/payment/settings", `{"offline_enabled":true,"offline_instructions":"转账"}`)
	plan := e.createPlan(t, `{"name":"专业版","entitlements":{"growth.xp_bonus":50},"prices":[{"duration_days":30,"price_cents":1000}]}`)
	var price struct{ ID uint }
	e.db.Table("membership_prices").Where("plan_id = ?", plan).First(&price)
	drain := func() {
		for i := 0; i < 50; i++ {
			if ran, err := e.app.Jobs.RunOnce(context.Background()); err != nil || !ran {
				return
			}
		}
	}
	xp := func(u *models.User, rule string) (base, final int64) {
		row := struct{ Base, Final int64 }{}
		e.db.Model(&models.ExperienceEvent{}).Where("user_id = ? AND rule_key = ?", u.ID, rule).
			Select("COALESCE(SUM(base_xp),0) AS base, COALESCE(SUM(final_xp),0) AS final").Scan(&row)
		return row.Base, row.Final
	}

	alice := e.user(t, "linker")
	// 未开通会员：没有加成
	plugincore.FireActivity(e.app, plugincore.ActivityEvent{UserID: alice.ID, Type: "comment.created", SourceType: "comment", SourceID: "c1", DedupeKey: "comment.created:c1"})
	if b, f := xp(alice, "community.comment"); b != 3 || f != 3 {
		t.Fatalf("非会员评论经验应为 3/3，实际 %d/%d", b, f)
	}

	// 付款开通：购买会员经验按加成放大（50 × 150%）
	status, p := e.doAs(t, alice, http.MethodPost, "/api/v1/payment/orders", fmt.Sprintf(`{"kind":"membership","sku":"%d","channel":"offline"}`, price.ID))
	if status != http.StatusOK {
		t.Fatalf("下单失败: %d %v", status, p)
	}
	orderNo := data(p)["order"].(map[string]any)["order_no"].(string)
	if status, p := e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+orderNo+"/confirm", ""); status != http.StatusOK {
		t.Fatalf("确认收款失败: %d %v", status, p)
	}
	if b, f := xp(alice, "membership.purchased"); b != 50 || f != 75 {
		t.Fatalf("购买会员经验应为基础 50、实得 75，实际 %d/%d", b, f)
	}
	// 会员期间：评论经验 3 × 150% = 4（取整）
	plugincore.FireActivity(e.app, plugincore.ActivityEvent{UserID: alice.ID, Type: "comment.created", SourceType: "comment", SourceID: "c2", DedupeKey: "comment.created:c2"})
	if b, f := xp(alice, "community.comment"); b != 6 || f != 7 {
		t.Fatalf("会员评论经验应累计基础 6、实得 7，实际 %d/%d", b, f)
	}
	values, sources := e.entitlements(t, alice)
	if values["growth.xp_bonus"] != 50 || sources["growth.xp_bonus"] != "membership" {
		t.Fatalf("经验加成应来自会员方案: %v %v", values["growth.xp_bonus"], sources["growth.xp_bonus"])
	}

	// 成就：会员指标驱动「尊享之始」
	drain()
	var first models.AchievementDefinition
	if e.db.Where("achievement_key = ?", "preset.membership.days.1").First(&first).Error != nil {
		t.Fatal("启用成就时应安装会员预设")
	}
	var granted int64
	e.db.Model(&models.UserAchievement{}).Where("user_id = ? AND achievement_id = ? AND revoked_at IS NULL", alice.ID, first.ID).Count(&granted)
	if granted != 1 {
		t.Fatalf("首次成为会员应解锁「尊享之始」，实际 %d", granted)
	}

	// 退款撤销：收回购买会员经验
	if status, p := e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+orderNo+"/refunds", `{"amount_cents":1000,"reason":"退款","revoke":true}`); status != http.StatusOK {
		t.Fatalf("退款失败: %d %v", status, p)
	}
	if _, f := xp(alice, "membership.purchased"); f != 0 {
		t.Fatalf("退款撤销后应收回购买经验，实际净经验 %d", f)
	}

	// 管理员不因「不受限制」获得最大加成
	var admin models.User
	e.db.Where("username = ?", "member-admin").First(&admin)
	if v := plugincore.EntitlementValue(e.app, &admin, "growth.xp_bonus"); v != 0 {
		t.Fatalf("管理员的经验加成应按基础值计算，实际 %d", v)
	}
}
