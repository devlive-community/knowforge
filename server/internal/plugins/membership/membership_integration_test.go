package membership_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"gorm.io/gorm"

	"knowforge/server/internal/app"
	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugins/membership"
	"knowforge/server/internal/testdb"
)

// 集成测试：启动完整应用，经插件管理接口启用会员插件（建表、权限），通过 HTTP 接口验证方案、开通与权益。

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
		t.Fatalf("加载配置失败: %v", err)
	}
	a, err := app.New(cfg)
	if err != nil {
		t.Fatalf("创建应用失败: %v", err)
	}
	e := &testEnv{app: a, server: httptest.NewServer(a.Router()), client: &http.Client{Timeout: 10 * time.Second}}
	t.Cleanup(e.server.Close)
	status, installed := e.request(t, "", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"会员测试"},"admin":{"username":"member-admin","email":"member-admin@test.local","password":"secret123"}}`)
	if status != http.StatusOK {
		t.Fatalf("安装失败: %d %v", status, installed)
	}
	e.token = installed["data"].(map[string]any)["token"].(string)
	e.db = a.DB
	e.setPlugin(t, "membership", true)
	return e
}

func (e *testEnv) request(t *testing.T, token, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, e.server.URL+path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	payload := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&payload)
	return resp.StatusCode, payload
}

// do 以管理员身份请求。
func (e *testEnv) do(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	return e.request(t, e.token, method, path, body)
}

func (e *testEnv) doAs(t *testing.T, u *models.User, method, path, body string) (int, map[string]any) {
	t.Helper()
	token, _ := auth.GenerateToken(e.app.Config.Secret, u.ID, u.Username, u.Role)
	return e.request(t, token, method, path, body)
}

func (e *testEnv) setPlugin(t *testing.T, key string, enabled bool) {
	t.Helper()
	action := "uninstall"
	if enabled {
		action = "install"
	}
	if status, payload := e.do(t, http.MethodPost, "/api/v1/admin/plugins/"+key+"/"+action, ""); status != http.StatusOK {
		t.Fatalf("%s %s 插件失败: %d %v", action, key, status, payload)
	}
}

func (e *testEnv) user(t *testing.T, username string) *models.User {
	t.Helper()
	u := &models.User{Username: username, Email: username + "@test.local", Role: "user", IsActive: true, EmailVerified: true}
	if err := e.db.Create(u).Error; err != nil {
		t.Fatal(err)
	}
	return u
}

func data(payload map[string]any) map[string]any {
	d, _ := payload["data"].(map[string]any)
	return d
}

func (e *testEnv) createPlan(t *testing.T, body string) uint {
	t.Helper()
	status, payload := e.do(t, http.MethodPost, "/api/v1/admin/membership/plans", body)
	if status != http.StatusOK {
		t.Fatalf("创建方案失败: %d %v", status, payload)
	}
	return uint(data(payload)["id"].(float64))
}

// entitlements 用户当前权益的取值与来源。
func (e *testEnv) entitlements(t *testing.T, u *models.User) (map[string]float64, map[string]string) {
	t.Helper()
	_, mine := e.doAs(t, u, http.MethodGet, "/api/v1/users/me/entitlements", "")
	values, sources := map[string]float64{}, map[string]string{}
	for _, it := range data(mine)["items"].([]any) {
		r := it.(map[string]any)
		values[r["key"].(string)] = r["value"].(float64)
		sources[r["key"].(string)] = r["source"].(string)
	}
	return values, sources
}

func (e *testEnv) membership(t *testing.T, userID uint) membership.UserMembership {
	t.Helper()
	var m membership.UserMembership
	e.db.Where("user_id = ?", userID).First(&m)
	return m
}

func near(t *testing.T, got, want time.Time, what string) {
	t.Helper()
	if d := got.Sub(want); d < -time.Minute || d > time.Minute {
		t.Fatalf("%s 应约为 %v，实际 %v", what, want, got)
	}
}

// 方案 CRUD 与校验；开通/续期/更换/调整/取消；会员权益独占（优先于等级，未配置的项回退基础值）；到期后失效。
func TestMembershipLifecycleAndEntitlements(t *testing.T) {
	e := newTestEnv(t)
	u := e.user(t, "member-user")

	// 校验：重复时长、未知权益、非正价格、划线价低于售价
	for _, bad := range []string{
		`{"name":"x","prices":[{"duration_days":30,"price_cents":100},{"duration_days":30,"price_cents":200}]}`,
		`{"name":"x","entitlements":{"no.such":1}}`,
		`{"name":"x","prices":[{"duration_days":30,"price_cents":0}]}`,
		`{"name":"x","prices":[{"duration_days":30,"price_cents":500,"original_price_cents":100}]}`,
		`{"name":""}`,
	} {
		if status, _ := e.do(t, http.MethodPost, "/api/v1/admin/membership/plans", bad); status != http.StatusBadRequest {
			t.Fatalf("非法方案应 400: %s → %d", bad, status)
		}
	}
	pro := e.createPlan(t, `{"name":"专业版","description":"更多书籍","entitlements":{"books.max":50,"collect.site":1},"prices":[{"duration_days":30,"price_cents":1900},{"duration_days":365,"price_cents":19900,"original_price_cents":22800}]}`)
	basic := e.createPlan(t, `{"name":"基础版","entitlements":{"books.max":10},"prices":[{"duration_days":30,"price_cents":900}]}`)

	// 更新价格：保留 30 天（改价）、删除 365 天、新增 90 天
	var prices []membership.Price
	e.db.Where("plan_id = ?", pro).Order("duration_days").Find(&prices)
	body := fmt.Sprintf(`{"name":"专业版","entitlements":{"books.max":50,"collect.site":1},"prices":[{"id":%d,"duration_days":30,"price_cents":2900},{"duration_days":90,"price_cents":6900}]}`, prices[0].ID)
	if status, payload := e.do(t, http.MethodPut, "/api/v1/admin/membership/plans/"+strconv.Itoa(int(pro)), body); status != http.StatusOK {
		t.Fatalf("更新方案失败: %d %v", status, payload)
	}
	var after []membership.Price
	e.db.Where("plan_id = ?", pro).Order("duration_days").Find(&after)
	if len(after) != 2 || after[0].ID != prices[0].ID || after[0].PriceCents != 2900 || after[1].DurationDays != 90 {
		t.Fatalf("价格应按请求整体替换（保留 id 更新、新增、删除缺失）: %+v", after)
	}

	// 公开方案列表
	_, plans := e.doAs(t, u, http.MethodGet, "/api/v1/membership/plans", "")
	if items := data(plans)["items"].([]any); len(items) != 2 || data(plans)["currency"] != "CNY" {
		t.Fatalf("公开方案列表错误: %v", plans)
	}

	// 基础收紧 + 成长等级：Lv.1 协作者 5 人（会员有效期内不叠加等级）
	e.do(t, http.MethodPut, "/api/v1/admin/entitlements/base", `{"values":{"books.max":3,"collaborators.max":1,"collect.site":0}}`)
	e.setPlugin(t, "growth", true)
	var lv1 models.LevelDefinition
	e.db.Where("level = 1").First(&lv1)
	if status, payload := e.do(t, http.MethodPut, "/api/v1/admin/growth/levels/"+strconv.Itoa(int(lv1.ID)), `{"level":1,"name":"Lv.1","min_xp":0,"status":"active","entitlements":{"collaborators.max":5}}`); status != http.StatusOK {
		t.Fatalf("保存等级权益失败: %d %v", status, payload)
	}
	if v, s := e.entitlements(t, u); v["collaborators.max"] != 5 || s["collaborators.max"] != "level" {
		t.Fatalf("非会员应取等级权益: %v %v", v, s)
	}

	// 开通 → 续期（同方案顺延）
	grant := func(plan uint, days int) map[string]any {
		t.Helper()
		status, payload := e.do(t, http.MethodPost, "/api/v1/admin/membership/grant", fmt.Sprintf(`{"user_id":%d,"plan_id":%d,"days":%d,"reason":"test"}`, u.ID, plan, days))
		if status != http.StatusOK {
			t.Fatalf("开通失败: %d %v", status, payload)
		}
		return data(payload)
	}
	now := time.Now()
	if r := grant(pro, 30); r["action"] != "grant" {
		t.Fatalf("首次应为 grant: %v", r)
	}
	near(t, e.membership(t, u.ID).ExpiresAt, now.Add(30*24*time.Hour), "开通到期时间")
	if r := grant(pro, 30); r["action"] != "extend" {
		t.Fatalf("同方案应为 extend: %v", r)
	}
	near(t, e.membership(t, u.ID).ExpiresAt, now.Add(60*24*time.Hour), "续期到期时间")

	v, s := e.entitlements(t, u)
	if v["books.max"] != 50 || s["books.max"] != "membership" || v["collect.site"] != 1 {
		t.Fatalf("会员权益应生效: %v %v", v, s)
	}
	if v["collaborators.max"] != 1 || s["collaborators.max"] != "base" {
		t.Fatalf("会员未配置的项应回退基础值（不叠加等级）: %v %v", v, s)
	}
	_, me := e.doAs(t, u, http.MethodGet, "/api/v1/auth/me", "")
	if ent := data(me)["entitlements"].(map[string]any); ent["books.max"].(float64) != 50 {
		t.Fatalf("/auth/me 应下发会员权益: %v", ent)
	}
	_, mine := e.doAs(t, u, http.MethodGet, "/api/v1/users/me/membership", "")
	m := data(mine)["membership"].(map[string]any)
	if m["active"] != true || m["plan"].(map[string]any)["name"] != "专业版" || len(data(mine)["records"].([]any)) != 2 {
		t.Fatalf("我的会员错误: %v", mine)
	}

	// 更换方案：从现在起按新方案计算
	if r := grant(basic, 30); r["action"] != "switch" {
		t.Fatalf("有效期内换方案应为 switch: %v", r)
	}
	near(t, e.membership(t, u.ID).ExpiresAt, now.Add(30*24*time.Hour), "换方案到期时间")
	if v, _ := e.entitlements(t, u); v["books.max"] != 10 {
		t.Fatalf("换方案后应按新方案: %v", v)
	}

	// 调整：直接设置方案与到期时间
	target := time.Now().AddDate(0, 0, 10).UTC().Truncate(time.Second)
	if status, payload := e.do(t, http.MethodPut, "/api/v1/admin/membership/members/"+strconv.Itoa(int(u.ID)), fmt.Sprintf(`{"plan_id":%d,"expires_at":"%s"}`, pro, target.Format(time.RFC3339))); status != http.StatusOK {
		t.Fatalf("调整失败: %d %v", status, payload)
	}
	if got := e.membership(t, u.ID); got.PlanID != pro || !got.ExpiresAt.Equal(target) {
		t.Fatalf("调整未生效: %+v", got)
	}
	if status, _ := e.do(t, http.MethodPut, "/api/v1/admin/membership/members/"+strconv.Itoa(int(u.ID)), fmt.Sprintf(`{"plan_id":%d,"expires_at":"2000-01-01T00:00:00Z"}`, pro)); status != http.StatusBadRequest {
		t.Fatalf("过去的到期时间应 400，实际 %d", status)
	}

	// 删除：有人持有 → 409；归档后不能再开通
	if status, _ := e.do(t, http.MethodDelete, "/api/v1/admin/membership/plans/"+strconv.Itoa(int(pro)), ""); status != http.StatusConflict {
		t.Fatalf("有持有人的方案删除应 409，实际 %d", status)
	}
	e.do(t, http.MethodPut, "/api/v1/admin/membership/plans/"+strconv.Itoa(int(basic)), `{"name":"基础版","status":"archived","prices":[{"duration_days":30,"price_cents":900}]}`)
	if status, _ := e.do(t, http.MethodPost, "/api/v1/admin/membership/grant", fmt.Sprintf(`{"user_id":%d,"plan_id":%d,"days":30}`, u.ID, basic)); status != http.StatusBadRequest {
		t.Fatalf("归档方案不能开通，实际 %d", status)
	}
	_, plans = e.doAs(t, u, http.MethodGet, "/api/v1/membership/plans", "")
	if items := data(plans)["items"].([]any); len(items) != 1 {
		t.Fatalf("公开列表不应包含归档方案: %v", items)
	}
	if status, _ := e.do(t, http.MethodDelete, "/api/v1/admin/membership/plans/"+strconv.Itoa(int(basic)), ""); status != http.StatusOK {
		t.Fatalf("无人持有的方案应可删除，实际 %d", status)
	}

	// 到期：权益回到等级/基础
	e.db.Model(&membership.UserMembership{}).Where("user_id = ?", u.ID).Update("expires_at", time.Now().Add(-time.Minute))
	if v, s := e.entitlements(t, u); v["books.max"] != 3 || v["collaborators.max"] != 5 || s["collaborators.max"] != "level" {
		t.Fatalf("到期后会员权益应失效: %v %v", v, s)
	}

	// 取消
	e.db.Model(&membership.UserMembership{}).Where("user_id = ?", u.ID).Update("expires_at", time.Now().AddDate(0, 0, 5))
	if status, _ := e.do(t, http.MethodPost, "/api/v1/admin/membership/members/"+strconv.Itoa(int(u.ID))+"/revoke", `{"reason":"test"}`); status != http.StatusOK {
		t.Fatalf("取消失败: %d", status)
	}
	if v, _ := e.entitlements(t, u); v["books.max"] != 3 {
		t.Fatalf("取消后会员权益应失效: %v", v)
	}
	var records int64
	e.db.Model(&membership.Record{}).Where("user_id = ?", u.ID).Count(&records)
	if records != 5 { // grant、extend、switch、adjust、revoke
		t.Fatalf("会员流水应有 5 条，实际 %d", records)
	}
	var notices int64
	e.db.Model(&models.Notification{}).Where("user_id = ? AND type = ?", u.ID, "membership").Count(&notices)
	if notices != 5 {
		t.Fatalf("开通/续期/换方案/调整/取消应各通知一次，实际 %d", notices)
	}

	// 禁用插件：会员权益不再生效、接口 404
	grant(pro, 30)
	e.setPlugin(t, "membership", false)
	if v, _ := e.entitlements(t, u); v["books.max"] != 3 {
		t.Fatalf("插件禁用后会员权益不应生效: %v", v)
	}
	if status, _ := e.doAs(t, u, http.MethodGet, "/api/v1/membership/plans", ""); status != http.StatusNotFound {
		t.Fatalf("插件禁用后接口应 404，实际 %d", status)
	}
}

// 到期提醒：到期前 N 天提醒一次、到期后通知一次，均不重复；续期后重新计算。
func TestMembershipExpiryReminders(t *testing.T) {
	e := newTestEnv(t)
	u := e.user(t, "reminder-user")
	plan := e.createPlan(t, `{"name":"月卡","prices":[{"duration_days":30,"price_cents":1000}]}`)
	count := func(key string) int64 {
		var n int64
		e.db.Model(&models.Notification{}).Where("user_id = ? AND type = ? AND payload LIKE ?", u.ID, "membership", "%"+key+"%").Count(&n)
		return n
	}
	e.do(t, http.MethodPost, "/api/v1/admin/membership/grant", fmt.Sprintf(`{"user_id":%d,"plan_id":%d,"days":30}`, u.ID, plan))

	membership.Sweep(e.app)
	if count("notify.membership.expiring") != 0 {
		t.Fatal("离到期还远时不应提醒")
	}
	e.db.Model(&membership.UserMembership{}).Where("user_id = ?", u.ID).Update("expires_at", time.Now().Add(48*time.Hour))
	membership.Sweep(e.app)
	membership.Sweep(e.app)
	if n := count("notify.membership.expiring"); n != 1 {
		t.Fatalf("到期前应只提醒一次，实际 %d", n)
	}
	e.db.Model(&membership.UserMembership{}).Where("user_id = ?", u.ID).Update("expires_at", time.Now().Add(-time.Hour))
	membership.Sweep(e.app)
	membership.Sweep(e.app)
	if n := count("notify.membership.expired"); n != 1 {
		t.Fatalf("到期后应只通知一次，实际 %d", n)
	}

	// 重新开通后清空提醒标记，下一周期可再次提醒
	e.do(t, http.MethodPost, "/api/v1/admin/membership/grant", fmt.Sprintf(`{"user_id":%d,"plan_id":%d,"days":1}`, u.ID, plan))
	membership.Sweep(e.app)
	if n := count("notify.membership.expiring"); n != 2 {
		t.Fatalf("新周期应再次提醒，实际 %d", n)
	}

	// 关闭提醒
	if status, _ := e.do(t, http.MethodPut, "/api/v1/admin/membership/settings", `{"reminder_days":0,"currency":"usd"}`); status != http.StatusOK {
		t.Fatalf("保存设置失败: %d", status)
	}
	_, s := e.do(t, http.MethodGet, "/api/v1/admin/membership/settings", "")
	if data(s)["currency"] != "USD" || data(s)["reminder_days"].(float64) != 0 {
		t.Fatalf("设置未保存: %v", s)
	}
	if status, _ := e.do(t, http.MethodPut, "/api/v1/admin/membership/settings", `{"currency":"US"}`); status != http.StatusBadRequest {
		t.Fatalf("非法货币应 400，实际 %d", status)
	}
}

// 在线购买（经支付插件，仅通过 HTTP 协作）：下单快照价格；方案随后归档仍按已付订单开通；重复确认不重复开通。
func TestMembershipPurchaseViaPayment(t *testing.T) {
	e := newTestEnv(t)
	u := e.user(t, "buyer")
	e.setPlugin(t, "payment", true)
	e.do(t, http.MethodPut, "/api/v1/admin/payment/settings", `{"offline_enabled":true,"offline_instructions":"转账至 6222"}`)
	plan := e.createPlan(t, `{"name":"年卡","entitlements":{"books.max":20},"prices":[{"duration_days":365,"price_cents":9900}]}`)
	var price membership.Price
	e.db.Where("plan_id = ?", plan).First(&price)

	_, prod := e.doAs(t, u, http.MethodGet, fmt.Sprintf("/api/v1/payment/products/membership/%d", price.ID), "")
	p := data(prod)["product"].(map[string]any)
	if p["title"] != "年卡" || p["amount_cents"].(float64) != 9900 || p["duration_days"].(float64) != 365 || p["return_link"] != "/user/membership" {
		t.Fatalf("会员商品解析错误: %v", p)
	}
	status, created := e.doAs(t, u, http.MethodPost, "/api/v1/payment/orders", fmt.Sprintf(`{"kind":"membership","sku":"%d","channel":"offline"}`, price.ID))
	if status != http.StatusOK {
		t.Fatalf("下单失败: %d %v", status, created)
	}
	no := data(created)["order"].(map[string]any)["order_no"].(string)

	// 下单后改价并归档方案：已下单订单按快照履约
	e.do(t, http.MethodPut, "/api/v1/admin/membership/plans/"+strconv.Itoa(int(plan)), fmt.Sprintf(`{"name":"年卡","status":"archived","entitlements":{"books.max":20},"prices":[{"id":%d,"duration_days":30,"price_cents":100}]}`, price.ID))
	if status, _ := e.doAs(t, u, http.MethodGet, fmt.Sprintf("/api/v1/payment/products/membership/%d", price.ID), ""); status != http.StatusBadRequest {
		t.Fatalf("归档方案不可再购买，实际 %d", status)
	}
	if status, payload := e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no+"/confirm", ""); status != http.StatusOK {
		t.Fatalf("确认收款失败: %d %v", status, payload)
	}
	m := e.membership(t, u.ID)
	near(t, m.ExpiresAt, time.Now().Add(365*24*time.Hour), "购买后到期时间")
	var rec membership.Record
	e.db.Where("user_id = ?", u.ID).First(&rec)
	if rec.Source != "order" || rec.SourceRef != no || rec.Days != 365 {
		t.Fatalf("会员流水应记录订单来源: %+v", rec)
	}
	if v, s := e.entitlements(t, u); v["books.max"] != 20 || s["books.max"] != "membership" {
		t.Fatalf("购买后会员权益应生效: %v %v", v, s)
	}
	// 重复确认 → 409，且不重复开通
	e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no+"/confirm", "")
	var n int64
	e.db.Model(&membership.Record{}).Where("user_id = ?", u.ID).Count(&n)
	if n != 1 {
		t.Fatalf("同一订单只能开通一次，实际流水 %d 条", n)
	}

	// 退款：部分退款并撤销 → 按比例扣回天数；再退剩余并撤销 → 会员结束
	status, r1 := e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no+"/refunds", `{"amount_cents":4950,"reason":"半价补偿","revoke":true}`)
	if status != http.StatusOK || data(r1)["status"] != "succeeded" || data(r1)["settled_at"] == nil {
		t.Fatalf("部分退款失败: %d %v", status, r1)
	}
	m = e.membership(t, u.ID)
	near(t, m.ExpiresAt, time.Now().Add((365-183)*24*time.Hour), "部分退款后到期时间（扣回 183 天）")
	var adj membership.Record
	e.db.Where("user_id = ? AND source = ?", u.ID, "refund").First(&adj)
	if adj.Action != membership.ActionAdjust || adj.Days != 183 || adj.SourceRef != data(r1)["refund_no"] {
		t.Fatalf("退款流水异常: %+v", adj)
	}
	// 重复回调幂等（管理员重试已完成的回调被拒绝，流水不重复）
	if status, _ := e.do(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/payment/refunds/%d/settle", uint(data(r1)["id"].(float64))), ""); status != http.StatusConflict {
		t.Fatalf("已完成的回调不能重试: %d", status)
	}
	if status, r2 := e.do(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no+"/refunds", `{"amount_cents":4950,"reason":"全部退款","revoke":true}`); status != http.StatusOK || data(r2)["status"] != "succeeded" {
		t.Fatalf("退剩余失败: %d %v", status, r2)
	}
	var left int64
	e.db.Model(&membership.UserMembership{}).Where("user_id = ?", u.ID).Count(&left)
	if left != 0 {
		t.Fatal("全部退款并撤销后会员应结束")
	}
	if v, s := e.entitlements(t, u); s["books.max"] == "membership" {
		t.Fatalf("会员结束后不应再有会员权益: %v %v", v, s)
	}
}
