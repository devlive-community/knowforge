package membership_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins/membership"
)

// fakeCharger 模拟已签约的周期扣款：只对登记的用户就绪，按预设结果扣款，成功时经商品提供者履约。
type fakeCharger struct {
	mu      sync.Mutex
	ready   map[uint]bool
	results map[uint][]error
	calls   map[uint]int
}

var charger = &fakeCharger{ready: map[uint]bool{}, results: map[uint][]error{}, calls: map[uint]int{}}

func init() {
	plugincore.RegisterRecurringCharger(plugincore.RecurringCharger{
		Key: "fake",
		Ready: func(_ plugincore.Core, userID uint, _ string) bool {
			charger.mu.Lock()
			defer charger.mu.Unlock()
			return charger.ready[userID]
		},
		Charge: func(_ context.Context, core plugincore.Core, userID uint, kind, sku string) (string, error) {
			charger.mu.Lock()
			n := charger.calls[userID]
			charger.calls[userID]++
			var err error
			if n < len(charger.results[userID]) {
				err = charger.results[userID][n]
			}
			charger.mu.Unlock()
			if err != nil {
				return "", err
			}
			provider, _ := plugincore.ProductProviderFor(kind)
			product, perr := provider.Resolve(core, nil, sku)
			if perr != nil {
				return "", perr
			}
			orderNo := fmt.Sprintf("AUTO%d%d", userID, n)
			return orderNo, provider.Fulfill(core, userID, orderNo, product.Payload)
		},
	})
}

// 自动续费：设置校验、未签约时到期前一键续费通知（每周期一次、不再重复普通提醒）、签约后自动扣款（失败重试、多次失败提醒）、方案下架时自动关闭。
func TestAutoRenewal(t *testing.T) {
	e := newTestEnv(t)
	pro := e.createPlan(t, `{"name":"专业版","prices":[{"duration_days":30,"price_cents":1900},{"duration_days":365,"price_cents":16800}]}`)
	basic := e.createPlan(t, `{"name":"基础版","prices":[{"duration_days":30,"price_cents":900}]}`)
	var monthly, basicPrice membership.Price
	e.db.Where("plan_id = ? AND duration_days = ?", pro, 30).First(&monthly)
	e.db.Where("plan_id = ?", basic).First(&basicPrice)
	notices := func(u *models.User, key string) int64 {
		var n int64
		e.db.Model(&models.Notification{}).Where("user_id = ? AND payload LIKE ?", u.ID, "%"+key+"%").Count(&n)
		return n
	}
	grant := func(u *models.User, plan uint, days int) {
		t.Helper()
		if status, p := e.do(t, http.MethodPost, "/api/v1/admin/membership/grant", fmt.Sprintf(`{"user_id":%d,"plan_id":%d,"days":%d}`, u.ID, plan, days)); status != http.StatusOK {
			t.Fatalf("开通失败: %d %v", status, p)
		}
	}
	expireIn := func(u *models.User, d time.Duration) {
		e.db.Model(&membership.UserMembership{}).Where("user_id = ?", u.ID).Updates(map[string]any{"expires_at": time.Now().Add(d), "reminded_at": nil})
	}

	// 设置校验：没有会员、选了其他方案的价格都不能开启
	alice := e.user(t, "alice")
	if status, _ := e.doAs(t, alice, http.MethodPut, "/api/v1/users/me/membership/renewal", fmt.Sprintf(`{"enabled":true,"price_id":%d}`, monthly.ID)); status != http.StatusBadRequest {
		t.Fatalf("没有会员不能开启续费: %d", status)
	}
	grant(alice, pro, 30)
	if status, _ := e.doAs(t, alice, http.MethodPut, "/api/v1/users/me/membership/renewal", fmt.Sprintf(`{"enabled":true,"price_id":%d}`, basicPrice.ID)); status != http.StatusBadRequest {
		t.Fatalf("只能选当前方案的价格: %d", status)
	}
	status, p := e.doAs(t, alice, http.MethodPut, "/api/v1/users/me/membership/renewal", fmt.Sprintf(`{"enabled":true,"price_id":%d}`, monthly.ID))
	if status != http.StatusOK || data(p)["enabled"] != true || data(p)["mode"] != "notify" || data(p)["next_renew_at"] == nil {
		t.Fatalf("开启续费失败: %d %v", status, p)
	}

	// 未签约：到期前 3 天内发一键续费通知（链接直达结算页），不再发普通到期提醒，也不重复
	expireIn(alice, 5*24*time.Hour)
	membership.Sweep(e.app)
	if notices(alice, "renewalDue") != 0 {
		t.Fatal("未进入续费窗口不应通知")
	}
	expireIn(alice, 2*24*time.Hour)
	membership.Sweep(e.app)
	membership.Sweep(e.app)
	var n models.Notification
	e.db.Where("user_id = ? AND payload LIKE ?", alice.ID, "%renewalDue%").First(&n)
	if notices(alice, "renewalDue") != 1 || notices(alice, "notify.membership.expiring") != 0 ||
		!containsAll(n.Payload, "/pay/checkout?kind=membership", "sku="+strconv.Itoa(int(monthly.ID))) {
		t.Fatalf("应发且只发一次一键续费通知: %s", n.Payload)
	}

	// 已签约：到期前一天内自动扣款；第一次失败 24 小时后重试，成功续期 30 天，本周期不再扣款
	bob := e.user(t, "bob")
	grant(bob, pro, 30)
	e.doAs(t, bob, http.MethodPut, "/api/v1/users/me/membership/renewal", fmt.Sprintf(`{"enabled":true,"price_id":%d}`, monthly.ID))
	charger.mu.Lock()
	charger.ready[bob.ID] = true
	charger.results[bob.ID] = []error{errors.New("余额不足")}
	charger.mu.Unlock()
	if _, p := e.doAs(t, bob, http.MethodGet, "/api/v1/users/me/membership/renewal", ""); data(p)["mode"] != "auto" {
		t.Fatalf("签约后应为自动扣款: %v", p)
	}
	expireIn(bob, 2*24*time.Hour)
	membership.Sweep(e.app)
	if charger.calls[bob.ID] != 0 {
		t.Fatal("自动扣款只在到期前一天内进行")
	}
	expireIn(bob, 12*time.Hour)
	membership.Sweep(e.app)
	membership.Sweep(e.app) // 重试时间未到
	var r membership.Renewal
	e.db.Where("user_id = ?", bob.ID).First(&r)
	if charger.calls[bob.ID] != 1 || r.Attempts != 1 || r.LastError != "余额不足" {
		t.Fatalf("首次扣款失败后应等待重试: %d %+v", charger.calls[bob.ID], r)
	}
	e.db.Model(&membership.Renewal{}).Where("user_id = ?", bob.ID).Update("next_attempt_at", time.Now().Add(-time.Minute))
	membership.Sweep(e.app)
	near(t, e.membership(t, bob.ID).ExpiresAt, time.Now().Add(12*time.Hour+30*24*time.Hour), "自动续期后的到期时间")
	membership.Sweep(e.app)
	e.db.Where("user_id = ?", bob.ID).First(&r)
	if charger.calls[bob.ID] != 2 || r.Attempts != 0 || r.LastOrderNo == "" {
		t.Fatalf("续期成功后本周期不应再扣款: %d %+v", charger.calls[bob.ID], r)
	}

	// 连续 3 次扣款失败：提醒手动续费
	carol := e.user(t, "carol")
	grant(carol, pro, 30)
	e.doAs(t, carol, http.MethodPut, "/api/v1/users/me/membership/renewal", fmt.Sprintf(`{"enabled":true,"price_id":%d}`, monthly.ID))
	charger.mu.Lock()
	charger.ready[carol.ID] = true
	charger.results[carol.ID] = []error{errors.New("卡已过期"), errors.New("卡已过期"), errors.New("卡已过期")}
	charger.mu.Unlock()
	expireIn(carol, 12*time.Hour)
	for i := 0; i < 3; i++ {
		e.db.Model(&membership.Renewal{}).Where("user_id = ?", carol.ID).Update("next_attempt_at", nil)
		membership.Sweep(e.app)
	}
	if charger.calls[carol.ID] != 3 || notices(carol, "renewalFailed") != 1 {
		t.Fatalf("3 次失败后应提醒手动续费: %d", charger.calls[carol.ID])
	}

	// 所选时长被删除：自动关闭续费计划并通知
	dave := e.user(t, "dave")
	grant(dave, basic, 30)
	e.doAs(t, dave, http.MethodPut, "/api/v1/users/me/membership/renewal", fmt.Sprintf(`{"enabled":true,"price_id":%d}`, basicPrice.ID))
	e.db.Delete(&membership.Price{}, basicPrice.ID)
	expireIn(dave, 2*24*time.Hour)
	membership.Sweep(e.app)
	if _, p := e.doAs(t, dave, http.MethodGet, "/api/v1/users/me/membership/renewal", ""); data(p)["enabled"] != false || notices(dave, "renewalDisabled") != 1 {
		t.Fatalf("价格删除后应关闭续费计划并通知: %v", p)
	}
	// 关闭续费
	if _, p := e.doAs(t, alice, http.MethodPut, "/api/v1/users/me/membership/renewal", `{"enabled":false}`); data(p)["enabled"] != false {
		t.Fatalf("关闭续费失败: %v", p)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
