package membership_test

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"knowforge/server/internal/plugins/membership"
)

// 兑换码：一次性卡密、活动码（多次、每人一次）、过期、停用、不区分大小写与连字符、并发不超用、输错限制、流水来源。
func TestRedeemCodes(t *testing.T) {
	e := newTestEnv(t)
	pro := e.createPlan(t, `{"name":"专业版","entitlements":{"books.max":50},"prices":[{"duration_days":30,"price_cents":1900}]}`)
	alice, bob, carol := e.user(t, "alice"), e.user(t, "bob"), e.user(t, "carol")

	// 校验
	for _, body := range []string{
		`{"name":"","plan_id":1,"days":30,"kind":"cards","count":1}`,
		fmt.Sprintf(`{"name":"x","plan_id":%d,"days":0,"kind":"cards","count":1}`, pro),
		fmt.Sprintf(`{"name":"x","plan_id":%d,"days":30,"kind":"cards","count":0}`, pro),
		fmt.Sprintf(`{"name":"x","plan_id":%d,"days":30,"kind":"promo","max_uses":5,"code":"a b!"}`, pro),
	} {
		if status, _ := e.do(t, http.MethodPost, "/api/v1/admin/membership/redeem/batches", body); status != http.StatusBadRequest {
			t.Fatalf("应拒绝: %s -> %d", body, status)
		}
	}

	// 一次性卡密：生成 3 个，列出与导出
	status, p := e.do(t, http.MethodPost, "/api/v1/admin/membership/redeem/batches", fmt.Sprintf(`{"name":"合作方赠送","plan_id":%d,"days":30,"kind":"cards","count":3}`, pro))
	if status != http.StatusOK || data(p)["codes"].(float64) != 3 || data(p)["plan_name"] != "专业版" {
		t.Fatalf("创建卡密失败: %d %v", status, p)
	}
	cardBatch := uint(data(p)["id"].(float64))
	_, list := e.do(t, http.MethodGet, fmt.Sprintf("/api/v1/admin/membership/redeem/batches/%d/codes", cardBatch), "")
	codes := data(list)["items"].([]any)
	first := codes[0].(map[string]any)["code"].(string)
	if len(codes) != 3 || len(first) != 19 || strings.Count(first, "-") != 3 {
		t.Fatalf("卡密格式异常: %v", codes)
	}

	// 兑换（小写、去掉连字符也可以）
	status, p = e.doAs(t, alice, http.MethodPost, "/api/v1/membership/redeem", fmt.Sprintf(`{"code":%q}`, strings.ToLower(strings.ReplaceAll(first, "-", ""))))
	if status != http.StatusOK || data(p)["action"] != "grant" {
		t.Fatalf("兑换失败: %d %v", status, p)
	}
	if m := e.membership(t, alice.ID); m.PlanID != pro || m.ExpiresAt.Sub(time.Now()) < 29*24*time.Hour {
		t.Fatalf("会员未开通: %+v", m)
	}
	var rec membership.Record
	e.db.Where("user_id = ?", alice.ID).Order("id DESC").First(&rec)
	if rec.Source != "redeem" || rec.SourceRef != first || rec.Reason != "合作方赠送" {
		t.Fatalf("流水来源异常: %+v", rec)
	}
	// 已用过的卡密不能再用；同一批次每人只能兑换一次
	if status, p := e.doAs(t, bob, http.MethodPost, "/api/v1/membership/redeem", fmt.Sprintf(`{"code":%q}`, first)); status != http.StatusBadRequest || !strings.Contains(p["message"].(string), "已被使用") {
		t.Fatalf("用过的卡密应拒绝: %d %v", status, p)
	}
	second := codes[1].(map[string]any)["code"].(string)
	if status, p := e.doAs(t, alice, http.MethodPost, "/api/v1/membership/redeem", fmt.Sprintf(`{"code":%q}`, second)); status != http.StatusBadRequest || !strings.Contains(p["message"].(string), "兑换过这一批") {
		t.Fatalf("同一批次每人只能兑换一次: %d %v", status, p)
	}
	// 停用单个码
	secondID := uint(codes[1].(map[string]any)["id"].(float64))
	e.do(t, http.MethodPut, fmt.Sprintf("/api/v1/admin/membership/redeem/codes/%d", secondID), `{"disabled":true}`)
	if status, p := e.doAs(t, bob, http.MethodPost, "/api/v1/membership/redeem", fmt.Sprintf(`{"code":%q}`, second)); status != http.StatusBadRequest || !strings.Contains(p["message"].(string), "停用") {
		t.Fatalf("停用的码应拒绝: %d %v", status, p)
	}

	// 活动码：自定义码，最多 2 人；并发兑换也不超用
	status, p = e.do(t, http.MethodPost, "/api/v1/admin/membership/redeem/batches", fmt.Sprintf(`{"name":"新年活动","plan_id":%d,"days":7,"kind":"promo","code":"new-year_2026","max_uses":2}`, pro))
	if status != http.StatusOK || data(p)["promo_code"] != "NEWYEAR_2026" {
		t.Fatalf("创建活动码失败: %d %v", status, p)
	}
	promoBatch := uint(data(p)["id"].(float64))
	if status, _ := e.do(t, http.MethodPost, "/api/v1/admin/membership/redeem/batches", fmt.Sprintf(`{"name":"重复","plan_id":%d,"days":7,"kind":"promo","code":"NEWYEAR_2026","max_uses":2}`, pro)); status != http.StatusBadRequest {
		t.Fatalf("重复的活动码应拒绝: %d", status)
	}
	var wg sync.WaitGroup
	results := make([]int, 3)
	for i, u := range []uint{alice.ID, bob.ID, carol.ID} {
		wg.Add(1)
		go func(i int, id uint) {
			defer wg.Done()
			var user = alice
			switch id {
			case bob.ID:
				user = bob
			case carol.ID:
				user = carol
			}
			results[i], _ = e.doAs(t, user, http.MethodPost, "/api/v1/membership/redeem", `{"code":"newyear_2026"}`)
		}(i, u)
	}
	wg.Wait()
	okCount := 0
	for _, s := range results {
		if s == http.StatusOK {
			okCount++
		}
	}
	var promo membership.RedeemCode
	e.db.Where("batch_id = ?", promoBatch).First(&promo)
	if okCount != 2 || promo.UsedCount != 2 {
		t.Fatalf("活动码应恰好兑换 2 次: 成功 %d 次，计数 %d，状态 %v", okCount, promo.UsedCount, results)
	}

	// 整批停用、过期
	e.do(t, http.MethodPut, fmt.Sprintf("/api/v1/admin/membership/redeem/batches/%d", cardBatch), `{"status":"disabled"}`)
	third := codes[2].(map[string]any)["code"].(string)
	if status, _ := e.doAs(t, carol, http.MethodPost, "/api/v1/membership/redeem", fmt.Sprintf(`{"code":%q}`, third)); status != http.StatusBadRequest {
		t.Fatalf("停用批次的码应拒绝: %d", status)
	}
	expires := time.Now().Add(time.Hour).Format(time.RFC3339)
	_, p = e.do(t, http.MethodPost, "/api/v1/admin/membership/redeem/batches", fmt.Sprintf(`{"name":"限时","plan_id":%d,"days":7,"kind":"promo","max_uses":10,"expires_at":%q}`, pro, expires))
	expiring := data(p)["promo_code"].(string)
	e.db.Model(&membership.RedeemBatch{}).Where("id = ?", uint(data(p)["id"].(float64))).Update("expires_at", time.Now().Add(-time.Minute))
	if status, p := e.doAs(t, carol, http.MethodPost, "/api/v1/membership/redeem", fmt.Sprintf(`{"code":%q}`, expiring)); status != http.StatusBadRequest || !strings.Contains(p["message"].(string), "过期") {
		t.Fatalf("过期的码应拒绝: %d %v", status, p)
	}

	// 输错限制
	for i := 0; i < 10; i++ {
		e.doAs(t, carol, http.MethodPost, "/api/v1/membership/redeem", `{"code":"WRONG-CODE"}`)
	}
	if status, p := e.doAs(t, carol, http.MethodPost, "/api/v1/membership/redeem", `{"code":"WRONG-CODE"}`); status != http.StatusTooManyRequests {
		t.Fatalf("输错过多应被限制: %d %v", status, p)
	}

	// 批次列表统计
	_, batches := e.do(t, http.MethodGet, "/api/v1/admin/membership/redeem/batches", "")
	for _, it := range data(batches)["items"].([]any) {
		b := it.(map[string]any)
		if uint(b["id"].(float64)) == promoBatch && b["redeemed"].(float64) != 2 {
			t.Fatalf("批次兑换人次异常: %v", b)
		}
	}
	// CSV 导出
	req, _ := http.NewRequest(http.MethodGet, e.server.URL+fmt.Sprintf("/api/v1/admin/membership/redeem/batches/%d/codes?format=csv", cardBatch), nil)
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := e.client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("导出失败: %v", err)
	}
	resp.Body.Close()
}
