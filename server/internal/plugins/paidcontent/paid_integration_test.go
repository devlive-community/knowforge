package paidcontent_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"knowforge/server/internal/app"
	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugins/paidcontent"
	"knowforge/server/internal/testdb"
)

// 集成测试（仅经 HTTP 与支付、会员插件协作）：付费墙与试读、导出拦截、购买章节/整本、作者入账与提现、会员权益免费读与折扣。

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
	status, installed := e.req(t, "", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"付费测试"},"admin":{"username":"paid-admin","email":"paid-admin@test.local","password":"secret123"}}`)
	if status != http.StatusOK {
		t.Fatalf("安装失败: %d %v", status, installed)
	}
	e.token = installed["data"].(map[string]any)["token"].(string)
	e.db = a.DB
	for _, key := range []string{"paid-content", "payment", "membership"} {
		if status, p := e.admin(t, http.MethodPost, "/api/v1/admin/plugins/"+key+"/install", ""); status != http.StatusOK {
			t.Fatalf("启用 %s 失败: %d %v", key, status, p)
		}
	}
	e.admin(t, http.MethodPut, "/api/v1/admin/payment/settings", `{"offline_enabled":true,"offline_instructions":"转账"}`)
	return e
}

func (e *testEnv) req(t *testing.T, token, method, path, body string) (int, map[string]any) {
	t.Helper()
	r, _ := http.NewRequest(method, e.server.URL+path, bytes.NewReader([]byte(body)))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := e.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	p := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&p)
	return resp.StatusCode, p
}

func (e *testEnv) admin(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	return e.req(t, e.token, method, path, body)
}

func (e *testEnv) as(t *testing.T, u *models.User, method, path, body string) (int, map[string]any) {
	t.Helper()
	token, _ := auth.GenerateToken(e.app.Config.Secret, u.ID, u.Username, u.Role)
	return e.req(t, token, method, path, body)
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

// buy 读者下单（线下转账）并由管理员确认收款。
func (e *testEnv) buy(t *testing.T, u *models.User, kind string, sku uint) string {
	t.Helper()
	status, created := e.as(t, u, http.MethodPost, "/api/v1/payment/orders", fmt.Sprintf(`{"kind":"%s","sku":"%d","channel":"offline"}`, kind, sku))
	if status != http.StatusOK {
		t.Fatalf("下单失败: %d %v", status, created)
	}
	no := data(created)["order"].(map[string]any)["order_no"].(string)
	if status, p := e.admin(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no+"/confirm", ""); status != http.StatusOK {
		t.Fatalf("确认收款失败: %d %v", status, p)
	}
	return no
}

func TestPaidContentFlow(t *testing.T) {
	e := newTestEnv(t)
	author, reader, member := e.user(t, "author"), e.user(t, "reader"), e.user(t, "member")

	_, created := e.as(t, author, http.MethodPost, "/api/v1/books", `{"title":"付费之书","status":"published","is_public":true}`)
	bookID := uint(data(created)["id"].(float64))
	long := strings.Repeat("第一段内容。", 20) + "\n\n" + strings.Repeat("第二段内容。", 20) + "\n\n" + strings.Repeat("第三段秘密。", 20)
	mk := func(title string, order int) uint {
		_, d := e.as(t, author, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), fmt.Sprintf(`{"title":%q,"content":%q,"status":"published","sort_order":%d}`, title, long, order))
		return uint(data(d)["id"].(float64))
	}
	a, b, c := mk("A", 0), mk("B", 1), mk("C", 2)
	docURL := func(id uint) string { return fmt.Sprintf("/api/v1/documents/%d", id) }

	// 定价：非作者 403；价格校验；前 1 章免费、章节 3 元、整本 10 元、试读 30%
	if status, _ := e.as(t, reader, http.MethodPut, fmt.Sprintf("/api/v1/books/%d/paid-settings", bookID), `{"enabled":true,"book_price_cents":1000}`); status != http.StatusForbidden {
		t.Fatalf("非作者定价应 403，实际 %d", status)
	}
	if status, _ := e.as(t, author, http.MethodPut, fmt.Sprintf("/api/v1/books/%d/paid-settings", bookID), `{"enabled":true}`); status != http.StatusBadRequest {
		t.Fatalf("未设置价格应 400，实际 %d", status)
	}
	status, settings := e.as(t, author, http.MethodPut, fmt.Sprintf("/api/v1/books/%d/paid-settings", bookID),
		`{"enabled":true,"book_price_cents":1000,"chapter_price_cents":300,"free_chapters":1,"preview_percent":30,"free_tier":2}`)
	if status != http.StatusOK || len(data(settings)["docs"].([]any)) != 3 {
		t.Fatalf("保存定价失败: %d %v", status, settings)
	}

	// 付费墙：免费章节全文；付费章节只给试读 + 付费墙；游客同样；作者本人全文
	_, docA := e.as(t, reader, http.MethodGet, docURL(a), "")
	if data(docA)["content"] != long || data(docA)["paywall"] != nil {
		t.Fatal("前 1 章应免费")
	}
	_, docB := e.as(t, reader, http.MethodGet, docURL(b), "")
	pw, _ := data(docB)["paywall"].(map[string]any)
	preview, _ := data(docB)["content"].(string)
	if pw == nil || pw["locked"] != true || pw["chapter_final_cents"].(float64) != 300 || strings.Contains(preview, "秘密") || !strings.Contains(preview, "第一段") {
		t.Fatalf("付费章节应只给试读与付费墙: %v / %q", pw, preview)
	}
	_, guest := e.req(t, "", http.MethodGet, docURL(b), "")
	if gp, _ := data(guest)["paywall"].(map[string]any); gp == nil || gp["logged_in"] != false {
		t.Fatalf("游客也应看到付费墙: %v", data(guest))
	}
	_, own := e.as(t, author, http.MethodGet, docURL(b), "")
	if data(own)["content"] != long {
		t.Fatal("作者应可读全文")
	}
	// 整本导出：未全部解锁的读者不可导出
	_, opts := e.as(t, reader, http.MethodGet, fmt.Sprintf("/api/v1/books/%d/export/options", bookID), "")
	if data(opts)["can_export"] != false {
		t.Fatalf("未解锁全书时不可导出: %v", data(opts))
	}
	_, info := e.as(t, reader, http.MethodGet, fmt.Sprintf("/api/v1/paid/books/%d", bookID), "")
	if ids := data(info)["locked_doc_ids"].([]any); len(ids) != 2 || data(info)["can_read_all"] != false {
		t.Fatalf("读者应有 2 章未解锁: %v", data(info))
	}

	// 购买章节 B：解锁 B，作者入账（抽成 20%）
	if status, _ := e.as(t, author, http.MethodGet, fmt.Sprintf("/api/v1/payment/products/paid-doc/%d", b), ""); status != http.StatusBadRequest {
		t.Fatal("作者不能购买自己的书")
	}
	if status, _ := e.as(t, reader, http.MethodGet, fmt.Sprintf("/api/v1/payment/products/paid-doc/%d", a), ""); status != http.StatusBadRequest {
		t.Fatal("免费章节不能购买")
	}
	e.buy(t, reader, "paid-doc", b)
	_, docB = e.as(t, reader, http.MethodGet, docURL(b), "")
	if data(docB)["content"] != long {
		t.Fatal("购买后应解锁章节")
	}
	var entry paidcontent.LedgerEntry
	e.db.Where("author_id = ? AND kind = ?", author.ID, paidcontent.LedgerSale).First(&entry)
	if entry.GrossCents != 300 || entry.CommissionCents != 60 || entry.NetCents != 240 {
		t.Fatalf("入账错误: %+v", entry)
	}

	// 购买整本：全部解锁、可导出，已购后不能重复购买
	e.buy(t, reader, "paid-book", bookID)
	_, info = e.as(t, reader, http.MethodGet, fmt.Sprintf("/api/v1/paid/books/%d", bookID), "")
	if data(info)["can_read_all"] != true || data(info)["purchased_book"] != true {
		t.Fatalf("整本购买后应全部解锁: %v", data(info))
	}
	_, opts = e.as(t, reader, http.MethodGet, fmt.Sprintf("/api/v1/books/%d/export/options", bookID), "")
	if data(opts)["can_export"] != true {
		t.Fatal("全部解锁后应可导出")
	}
	if status, _ := e.as(t, reader, http.MethodGet, fmt.Sprintf("/api/v1/payment/products/paid-doc/%d", c), ""); status != http.StatusBadRequest {
		t.Fatal("已解锁的章节不能再购买")
	}
	_, purchases := e.as(t, reader, http.MethodGet, "/api/v1/users/me/purchases", "")
	if data(purchases)["total"].(float64) != 2 {
		t.Fatalf("应有 2 条购买记录: %v", data(purchases)["total"])
	}

	// 会员权益：访问等级 2 → 本书免费；折扣 50% 对其他付费书生效
	_, plan := e.admin(t, http.MethodPost, "/api/v1/admin/membership/plans", `{"name":"读者会员","entitlements":{"content.access_tier":2,"content.discount_percent":50}}`)
	planID := uint(data(plan)["id"].(float64))
	e.admin(t, http.MethodPost, "/api/v1/admin/membership/grant", fmt.Sprintf(`{"user_id":%d,"plan_id":%d,"days":30}`, member.ID, planID))
	_, docC := e.as(t, member, http.MethodGet, docURL(c), "")
	if data(docC)["content"] != long {
		t.Fatal("内容访问等级达到书籍设定应免费阅读")
	}
	e.as(t, author, http.MethodPut, fmt.Sprintf("/api/v1/books/%d/paid-settings", bookID), `{"enabled":true,"book_price_cents":1000,"chapter_price_cents":300,"free_chapters":1,"preview_percent":30,"free_tier":0}`)
	_, docC = e.as(t, member, http.MethodGet, docURL(c), "")
	if pw, _ := data(docC)["paywall"].(map[string]any); pw == nil || pw["chapter_final_cents"].(float64) != 150 || pw["book_final_cents"].(float64) != 500 {
		t.Fatalf("会员折扣 50%% 应体现在付费墙价格: %v", data(docC)["paywall"])
	}
	_, prod := e.as(t, member, http.MethodGet, fmt.Sprintf("/api/v1/payment/products/paid-book/%d", bookID), "")
	if data(prod)["product"].(map[string]any)["amount_cents"].(float64) != 500 {
		t.Fatal("下单金额应按折扣计算")
	}

	// 提现：低于最低金额/超过余额 400；冻结 → 驳回退回 → 再申请 → 打款
	e.admin(t, http.MethodPut, "/api/v1/admin/paid/settings", `{"min_withdrawal_cents":100}`)
	_, earn := e.as(t, author, http.MethodGet, "/api/v1/users/me/earnings", "")
	balance := data(earn)["balance_cents"].(float64) // 240 + 800
	if balance != 1040 {
		t.Fatalf("余额应为 1040，实际 %v", balance)
	}
	if status, _ := e.as(t, author, http.MethodPost, "/api/v1/users/me/withdrawals", `{"amount_cents":50,"account":"支付宝 a@b.c"}`); status != http.StatusBadRequest {
		t.Fatal("低于最低提现金额应 400")
	}
	if status, _ := e.as(t, author, http.MethodPost, "/api/v1/users/me/withdrawals", `{"amount_cents":999999,"account":"支付宝 a@b.c"}`); status != http.StatusBadRequest {
		t.Fatal("超过余额应 400")
	}
	_, w := e.as(t, author, http.MethodPost, "/api/v1/users/me/withdrawals", `{"amount_cents":1000,"account":"支付宝 a@b.c"}`)
	wid := uint(data(w)["id"].(float64))
	if status, _ := e.as(t, author, http.MethodPost, "/api/v1/users/me/withdrawals", `{"amount_cents":10,"account":"x"}`); status != http.StatusBadRequest {
		t.Fatal("已有待处理提现时不能再申请")
	}
	_, earn = e.as(t, author, http.MethodGet, "/api/v1/users/me/earnings", "")
	if data(earn)["balance_cents"].(float64) != 40 {
		t.Fatal("申请后应冻结提现金额")
	}
	if status, _ := e.admin(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/paid/withdrawals/%d/reject", wid), `{}`); status != http.StatusBadRequest {
		t.Fatal("驳回需填写原因")
	}
	e.admin(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/paid/withdrawals/%d/reject", wid), `{"note":"收款信息不完整"}`)
	_, earn = e.as(t, author, http.MethodGet, "/api/v1/users/me/earnings", "")
	if data(earn)["balance_cents"].(float64) != 1040 {
		t.Fatal("驳回后金额应退回")
	}
	_, w = e.as(t, author, http.MethodPost, "/api/v1/users/me/withdrawals", `{"amount_cents":1040,"account":"支付宝 a@b.c 张三"}`)
	wid = uint(data(w)["id"].(float64))
	if status, _ := e.admin(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/paid/withdrawals/%d/pay", wid), `{"note":"已转账"}`); status != http.StatusOK {
		t.Fatal("确认打款失败")
	}
	if status, _ := e.admin(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/paid/withdrawals/%d/pay", wid), `{}`); status != http.StatusConflict {
		t.Fatal("已处理的提现不能重复处理")
	}
	_, sales := e.admin(t, http.MethodGet, "/api/v1/admin/paid/sales", "")
	if data(sales)["gross_cents"].(float64) != 1300 || data(sales)["commission_cents"].(float64) != 260 {
		t.Fatalf("销售合计错误: %v", data(sales))
	}

	// 插件禁用：全部内容恢复可读
	e.admin(t, http.MethodPost, "/api/v1/admin/plugins/paid-content/uninstall", "")
	_, docC = e.as(t, e.user(t, "late"), http.MethodGet, docURL(c), "")
	if data(docC)["content"] != long || data(docC)["paywall"] != nil {
		t.Fatal("插件禁用后内容应不受限")
	}
}

// 退款：按比例扣回作者净收益（可为负）；不撤销时仍可阅读，撤销后重新锁定；按退款单号幂等。
func TestPaidContentRefund(t *testing.T) {
	e := newTestEnv(t)
	author, reader := e.user(t, "author"), e.user(t, "reader")
	_, created := e.as(t, author, http.MethodPost, "/api/v1/books", `{"title":"退款之书","status":"published","is_public":true}`)
	bookID := uint(data(created)["id"].(float64))
	body := strings.Repeat("试读内容。", 20) + "\n\n" + strings.Repeat("付费秘密。", 20)
	_, d := e.as(t, author, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), fmt.Sprintf(`{"title":"付费章","content":%q,"status":"published"}`, body))
	docID := uint(data(d)["id"].(float64))
	e.as(t, author, http.MethodPut, fmt.Sprintf("/api/v1/books/%d/paid-settings", bookID), `{"enabled":true,"chapter_price_cents":300,"preview_percent":30}`)
	no := e.buy(t, reader, "paid-doc", docID)
	doc := func() map[string]any {
		_, p := e.as(t, reader, http.MethodGet, fmt.Sprintf("/api/v1/documents/%d", docID), "")
		return data(p)
	}
	if doc()["paywall"] != nil {
		t.Fatal("购买后应解锁")
	}
	balance := func() int64 {
		var sum struct{ Total int64 }
		e.db.Model(&paidcontent.LedgerEntry{}).Select("COALESCE(SUM(net_cents),0) AS total").Where("author_id = ?", author.ID).Scan(&sum)
		return sum.Total
	}
	if balance() != 240 {
		t.Fatalf("售出后作者净收益应为 240: %d", balance())
	}

	// 部分退款（不撤销）：扣回一半净收益，仍可阅读
	status, r1 := e.admin(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no+"/refunds", `{"amount_cents":150,"reason":"部分退款","revoke":false}`)
	if status != http.StatusOK || data(r1)["settled_at"] == nil {
		t.Fatalf("部分退款失败: %d %v", status, r1)
	}
	var entry paidcontent.LedgerEntry
	e.db.Where("kind = ? AND refund_no = ?", paidcontent.LedgerRefund, data(r1)["refund_no"]).First(&entry)
	if entry.GrossCents != -150 || entry.NetCents != -120 || entry.CommissionCents != -30 || balance() != 120 {
		t.Fatalf("退款流水异常: %+v 余额 %d", entry, balance())
	}
	if doc()["paywall"] != nil {
		t.Fatal("不撤销时应仍可阅读")
	}
	// 作者已提现后再退款：余额可为负
	e.db.Create(&paidcontent.LedgerEntry{AuthorID: author.ID, Kind: paidcontent.LedgerWithdrawal, NetCents: -120, Currency: "CNY"})
	if status, r2 := e.admin(t, http.MethodPost, "/api/v1/admin/payment/orders/"+no+"/refunds", `{"amount_cents":150,"reason":"全部退款","revoke":true}`); status != http.StatusOK || data(r2)["settled_at"] == nil {
		t.Fatalf("退剩余失败: %d %v", status, r2)
	}
	if balance() != -120 {
		t.Fatalf("已提现后退款余额应为负: %d", balance())
	}
	if pw, _ := doc()["paywall"].(map[string]any); pw == nil || pw["locked"] != true {
		t.Fatal("撤销后应重新锁定")
	}
	var n int64
	e.db.Model(&paidcontent.LedgerEntry{}).Where("kind = ?", paidcontent.LedgerRefund).Count(&n)
	if n != 2 {
		t.Fatalf("两次退款应各有一条流水: %d", n)
	}
}
