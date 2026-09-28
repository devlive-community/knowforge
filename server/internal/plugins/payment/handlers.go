package payment

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

func (b *behavior) Key() string { return plugins.KeyPayment }

func (b *behavior) RegisterRoutes(api *gin.RouterGroup, core plugincore.Core) {
	b.core = core
	feat := core.RequireFeaturePlugin(plugins.KeyPayment)
	user := []gin.HandlerFunc{core.RequireAuth(), feat, core.RequirePermissionMiddleware(PermOrder)}
	with := func(h gin.HandlerFunc) []gin.HandlerFunc { return append(append([]gin.HandlerFunc{}, user...), h) }
	api.GET("/payment/products/:kind/:sku", with(b.GetProduct)...)
	api.POST("/payment/orders", with(b.CreateOrder)...)
	api.GET("/payment/orders/:no", with(b.GetOrder)...)
	api.POST("/payment/orders/:no/cancel", with(b.CancelOrder)...)
	api.POST("/payment/orders/:no/proof", with(b.SubmitProof)...)
	api.POST("/payment/orders/:no/refund-request", with(b.RequestRefund)...)
	api.GET("/users/me/orders", with(b.MyOrders)...)
	// 渠道异步通知：公开、以签名校验身份；不受插件开关限制（已发起的支付仍需入账）
	api.POST("/payment/notify/:channel", b.Notify)
	// 管理员
	admin := []gin.HandlerFunc{core.RequireAuth(), core.RequireAdmin(), feat, core.RequirePermissionMiddleware(PermManage)}
	reg := func(method, path string, h gin.HandlerFunc) {
		api.Handle(method, path, append(append([]gin.HandlerFunc{}, admin...), h)...)
	}
	reg(http.MethodGet, "/admin/payment/orders", b.AdminListOrders)
	reg(http.MethodPost, "/admin/payment/orders/:no/confirm", b.AdminConfirm)
	reg(http.MethodPost, "/admin/payment/orders/:no/cancel", b.AdminCancel)
	reg(http.MethodPost, "/admin/payment/orders/:no/fulfill", b.AdminRetryFulfill)
	reg(http.MethodPost, "/admin/payment/orders/:no/refunds", b.AdminCreateRefund)
	reg(http.MethodGet, "/admin/payment/refunds", b.AdminListRefunds)
	reg(http.MethodPost, "/admin/payment/refunds/:id/approve", b.AdminApproveRefund)
	reg(http.MethodPost, "/admin/payment/refunds/:id/reject", b.AdminRejectRefund)
	reg(http.MethodPost, "/admin/payment/refunds/:id/sync", b.AdminSyncRefund)
	reg(http.MethodPost, "/admin/payment/refunds/:id/resolve", b.AdminResolveRefund)
	reg(http.MethodPost, "/admin/payment/refunds/:id/settle", b.AdminSettleRefund)
	reg(http.MethodGet, "/admin/payment/settings", b.AdminGetSettings)
	reg(http.MethodPut, "/admin/payment/settings", b.AdminUpdateSettings)
}

// baseURL 站点地址：优先「站点访问地址」设置；未配置时取请求的 Origin（前后端分离开发）或 Host。
func (b *behavior) baseURL(c *gin.Context) string {
	if v := strings.TrimRight(b.core.GetSetting("site_url"), "/"); v != "" {
		return v
	}
	if u, err := url.Parse(c.GetHeader("Origin")); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	scheme := "http"
	if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + c.Request.Host
}

// availableChannels 当前可用于该货币的支付方式。
func availableChannels(cfg config, currency string) []string {
	out := []string{}
	for _, ch := range allChannels {
		if ch.Available(cfg, currency) {
			out = append(out, ch.Key())
		}
	}
	return out
}

// GetProduct GET /payment/products/:kind/:sku 结算页：商品与可用支付方式。
func (b *behavior) GetProduct(c *gin.Context) {
	provider, found := plugincore.ProductProviderFor(c.Param("kind"))
	if !found {
		b.core.Fail(c, http.StatusNotFound, "商品不存在")
		return
	}
	u := b.core.CurrentUser(c)
	product, err := provider.Resolve(b.core, u, c.Param("sku"))
	if err != nil {
		b.core.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	dp, couponSupported := plugincore.DiscountProviderFor(b.core, product.Kind)
	resp := gin.H{"product": product, "channels": availableChannels(loadConfig(b.core), product.Currency), "coupon_supported": couponSupported}
	// ?coupon= 试算优惠（不占用次数；下单时才占用）
	if code := strings.TrimSpace(c.Query("coupon")); code != "" {
		if !couponSupported {
			b.core.Fail(c, http.StatusBadRequest, "该商品不能使用优惠码")
			return
		}
		d, err := dp.Quote(b.core, u, product, code)
		if err != nil {
			b.core.Fail(c, http.StatusBadRequest, err.Error())
			return
		}
		resp["discount"] = d
	}
	b.core.OK(c, resp)
}

// CreateOrder POST /payment/orders {kind, sku, channel, mobile}
func (b *behavior) CreateOrder(c *gin.Context) {
	var req struct {
		Kind    string `json:"kind"`
		SKU     string `json:"sku"`
		Channel string `json:"channel"`
		Coupon  string `json:"coupon"`
		Mobile  bool   `json:"mobile"`
	}
	if c.ShouldBindJSON(&req) != nil || req.Kind == "" || req.SKU == "" || req.Channel == "" {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	o, act, err := b.createOrder(c.Request.Context(), b.core.CurrentUser(c), req.Kind, req.SKU, req.Channel, req.Coupon, req.Mobile, b.baseURL(c))
	if err != nil {
		b.core.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	b.core.OK(c, gin.H{"order": o, "action": act})
}

// ownOrder 当前用户的订单（管理员可看任意订单）。
func (b *behavior) ownOrder(c *gin.Context) (*Order, bool) {
	var o Order
	u := b.core.CurrentUser(c)
	if b.core.Gorm().Where("order_no = ?", c.Param("no")).First(&o).Error != nil || (o.UserID != u.ID && !b.core.IsAdmin(u)) {
		b.core.Fail(c, http.StatusNotFound, "订单不存在")
		return nil, false
	}
	return &o, true
}

// GetOrder GET /payment/orders/:no 订单详情（待支付的在线订单会先向渠道同步一次）；待支付时附带继续支付的动作。
func (b *behavior) GetOrder(c *gin.Context) {
	o, found := b.ownOrder(c)
	if !found {
		return
	}
	b.sync(c.Request.Context(), o)
	refunds := b.refundsOf(o.ID)
	for i := range refunds {
		b.syncRefund(c.Request.Context(), &refunds[i])
	}
	b.core.Gorm().First(o, o.ID)
	out := gin.H{"order": o, "refunds": refunds, "refundable_cents": refundableCents(o)}
	if err := b.canRequestRefund(o); err == nil && o.UserID == b.core.CurrentUser(c).ID {
		out["can_request_refund"] = true
	} else if err != nil {
		out["refund_blocked_reason"] = err.Error()
	}
	if o.Status == StatusPending && time.Now().Before(o.ExpiresAt) {
		if ch, ok := channelFor(o.Channel); ok {
			if act, err := ch.Resume(createInput{Order: o, Cfg: loadConfig(b.core), BaseURL: b.baseURL(c), Mobile: c.Query("mobile") == "1"}); err == nil {
				out["action"] = act
			}
		}
	}
	b.core.OK(c, out)
}

// CancelOrder POST /payment/orders/:no/cancel 取消待支付订单。
func (b *behavior) CancelOrder(c *gin.Context) {
	o, found := b.ownOrder(c)
	if !found {
		return
	}
	res := b.core.Gorm().Model(&Order{}).Where("id = ? AND status = ?", o.ID, StatusPending).Update("status", StatusCancelled)
	if res.RowsAffected != 1 {
		b.core.Fail(c, http.StatusConflict, "订单当前状态不能取消")
		return
	}
	b.releaseDiscount(o)
	b.core.OK(c, gin.H{"message": "已取消"})
}

// SubmitProof POST /payment/orders/:no/proof {note} 线下转账：提交付款说明（如转账流水号/付款人），等待管理员确认。
func (b *behavior) SubmitProof(c *gin.Context) {
	o, found := b.ownOrder(c)
	if !found {
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.Note) == "" {
		b.core.Fail(c, http.StatusBadRequest, "请填写付款说明")
		return
	}
	if o.Channel != chOffline || o.Status != StatusPending {
		b.core.Fail(c, http.StatusConflict, "只有待确认的线下转账订单可以提交付款说明")
		return
	}
	now := time.Now()
	b.core.Gorm().Model(&Order{}).Where("id = ?", o.ID).Updates(map[string]any{"payer_note": truncateRunes(strings.TrimSpace(req.Note), 500), "proof_at": now})
	b.core.OK(c, gin.H{"message": "已提交"})
}

// RequestRefund POST /payment/orders/:no/refund-request {reason} 用户申请退款（支付后可申请期内，等待管理员审核）。
func (b *behavior) RequestRefund(c *gin.Context) {
	o, found := b.ownOrder(c)
	if !found {
		return
	}
	u := b.core.CurrentUser(c)
	if o.UserID != u.ID {
		b.core.Fail(c, http.StatusForbidden, "只能为自己的订单申请退款")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.Reason) == "" {
		b.core.Fail(c, http.StatusBadRequest, "请填写退款原因")
		return
	}
	r, err := b.requestRefund(u, o, strings.TrimSpace(req.Reason))
	if err != nil {
		b.core.Fail(c, http.StatusConflict, err.Error())
		return
	}
	b.core.OK(c, r)
}

// MyOrders GET /users/me/orders?page=&page_size=
func (b *behavior) MyOrders(c *gin.Context) {
	page, pageSize := b.core.Paginate(c)
	q := b.core.Gorm().Model(&Order{}).Where("user_id = ?", b.core.CurrentUser(c).ID)
	var total int64
	q.Count(&total)
	var rows []Order
	q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows)
	b.core.OK(c, plugincore.PageResult{Items: rows, Total: total, Page: page, PageSize: pageSize})
}

// Notify POST /payment/notify/:channel 渠道异步通知（验签后入账；无论成败都按渠道约定应答）。
func (b *behavior) Notify(c *gin.Context) {
	ch, found := channelFor(c.Param("channel"))
	if !found || ch.Key() == chOffline || !b.core.Gorm().Migrator().HasTable(&Order{}) {
		c.Status(http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 256<<10))
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	res, reply, err := ch.Notify(loadConfig(b.core), c.Request, body)
	if err != nil {
		log.Printf("[payment] %s 通知处理失败: %v", ch.Key(), err)
	} else if res != nil {
		if _, err := b.markPaid(res, ch.Key(), 0); err != nil {
			// 金额/订单不匹配：记录并按失败应答，由渠道重发、人工核查
			log.Printf("[payment] %s 通知入账失败: %v", ch.Key(), err)
			reply = failReply(ch.Key())
		}
	}
	if reply.Body == "" {
		c.Status(reply.Status)
		return
	}
	c.Data(reply.Status, reply.ContentType, []byte(reply.Body))
}

func failReply(channelKey string) notifyReply {
	if channelKey == chAlipay {
		return notifyReply{Status: http.StatusOK, ContentType: "text/plain", Body: "fail"}
	}
	return notifyReply{Status: http.StatusBadRequest, ContentType: "application/json", Body: `{"code":"FAIL","message":"入账失败"}`}
}

// —— 管理端 ——

type userBrief struct {
	ID          uint   `json:"id"`
	Username    string `json:"username"`
	Nickname    string `json:"nickname"`
	DisplayName string `json:"display_name"`
	Avatar      string `json:"avatar"`
}

// AdminListOrders GET /admin/payment/orders?status=&channel=&q=&awaiting=1&page=&page_size=
func (b *behavior) AdminListOrders(c *gin.Context) {
	page, pageSize := b.core.Paginate(c)
	db := b.core.Gorm()
	q := db.Model(&Order{})
	if s := c.Query("status"); s != "" {
		q = q.Where("status = ?", s)
	}
	if ch := c.Query("channel"); ch != "" {
		q = q.Where("channel = ?", ch)
	}
	if c.Query("awaiting") == "1" { // 待确认的线下转账
		q = q.Where("channel = ? AND status = ?", chOffline, StatusPending)
	}
	if c.Query("unfulfilled") == "1" {
		q = q.Where("status = ? AND fulfilled_at IS NULL", StatusPaid)
	}
	if kw := strings.TrimSpace(c.Query("q")); kw != "" {
		like := "%" + kw + "%"
		q = q.Where("order_no LIKE ? OR title LIKE ? OR channel_trade_no LIKE ? OR user_id IN (?)", like, like, like,
			db.Model(&models.User{}).Select("id").Where("username LIKE ? OR email LIKE ?", like, like))
	}
	var total int64
	q.Count(&total)
	var rows []Order
	q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows)
	ids := make([]uint, 0, len(rows))
	for _, o := range rows {
		ids = append(ids, o.UserID)
	}
	users := map[uint]userBrief{}
	if len(ids) > 0 {
		var list []models.User
		db.Select("id, username, nickname, name_display, avatar").Where("id IN ?", ids).Find(&list)
		for _, u := range list {
			users[u.ID] = userBrief{ID: u.ID, Username: u.Username, Nickname: u.Nickname, DisplayName: u.PublicName(), Avatar: u.Avatar}
		}
	}
	items := make([]gin.H, 0, len(rows))
	for i := range rows {
		items = append(items, gin.H{"order": rows[i], "user": users[rows[i].UserID], "refundable_cents": refundableCents(&rows[i])})
	}
	b.core.OK(c, plugincore.PageResult{Items: items, Total: total, Page: page, PageSize: pageSize})
}

func (b *behavior) findOrder(c *gin.Context) (*Order, bool) {
	var o Order
	if b.core.Gorm().Where("order_no = ?", c.Param("no")).First(&o).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "订单不存在")
		return nil, false
	}
	return &o, true
}

// AdminConfirm POST /admin/payment/orders/:no/confirm 确认线下转账已到账（置为已支付并履约）。
func (b *behavior) AdminConfirm(c *gin.Context) {
	o, found := b.findOrder(c)
	if !found {
		return
	}
	if o.Channel != chOffline || o.Status == StatusPaid || o.Status == StatusRefunded {
		b.core.Fail(c, http.StatusConflict, "只能确认未支付的线下转账订单")
		return
	}
	admin := b.core.CurrentUser(c)
	changed, err := b.markPaid(&paidResult{OrderNo: o.OrderNo, TradeNo: "manual:" + admin.Username, AmountCents: o.AmountCents, Currency: o.Currency}, chOffline, admin.ID)
	if err != nil || !changed {
		b.core.Fail(c, http.StatusConflict, fmt.Sprintf("确认失败：%v", errOr(err, "订单状态已变化")))
		return
	}
	b.core.RecordAudit(c, "payment.order_confirmed", "payment_order", o.OrderNo, o.Title, map[string]any{"changed_fields": []string{"status"}})
	b.respondOrder(c, o.ID)
}

// AdminCancel POST /admin/payment/orders/:no/cancel 取消待支付订单。
func (b *behavior) AdminCancel(c *gin.Context) {
	o, found := b.findOrder(c)
	if !found {
		return
	}
	res := b.core.Gorm().Model(&Order{}).Where("id = ? AND status IN ?", o.ID, []string{StatusPending, StatusExpired}).Update("status", StatusCancelled)
	if res.RowsAffected != 1 {
		b.core.Fail(c, http.StatusConflict, "订单当前状态不能取消")
		return
	}
	b.releaseDiscount(o)
	b.core.RecordAudit(c, "payment.order_cancelled", "payment_order", o.OrderNo, o.Title, map[string]any{"changed_fields": []string{"status"}})
	b.respondOrder(c, o.ID)
}

// AdminRetryFulfill POST /admin/payment/orders/:no/fulfill 重试履约（已支付但履约失败的订单）。
func (b *behavior) AdminRetryFulfill(c *gin.Context) {
	o, found := b.findOrder(c)
	if !found {
		return
	}
	if o.Status != StatusPaid || o.FulfilledAt != nil {
		b.core.Fail(c, http.StatusConflict, "只有已支付且未履约的订单可以重试")
		return
	}
	b.fulfill(o)
	b.core.RecordAudit(c, "payment.order_fulfilled", "payment_order", o.OrderNo, o.Title, map[string]any{"changed_fields": []string{"fulfilled_at"}})
	b.respondOrder(c, o.ID)
}

// —— 退款（管理端） ——

func (b *behavior) findRefund(c *gin.Context) (*Refund, bool) {
	var r Refund
	if b.core.Gorm().First(&r, c.Param("id")).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "退款不存在")
		return nil, false
	}
	return &r, true
}

func (b *behavior) refundAudit(c *gin.Context, action string, r *Refund, fields ...string) {
	b.core.RecordAudit(c, action, "payment_refund", r.RefundNo, r.OrderNo, map[string]any{"changed_fields": fields, "amount_cents": r.AmountCents, "status": r.Status})
}

// AdminCreateRefund POST /admin/payment/orders/:no/refunds {amount_cents, reason, revoke} 直接退款（可部分退款）。
func (b *behavior) AdminCreateRefund(c *gin.Context) {
	o, found := b.findOrder(c)
	if !found {
		return
	}
	var req struct {
		AmountCents int64  `json:"amount_cents"`
		Reason      string `json:"reason"`
		Revoke      bool   `json:"revoke"`
	}
	if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.Reason) == "" {
		b.core.Fail(c, http.StatusBadRequest, "请填写退款金额与原因")
		return
	}
	r, err := b.createRefund(c.Request.Context(), b.core.CurrentUser(c), o, req.AmountCents, strings.TrimSpace(req.Reason), req.Revoke)
	if err != nil {
		b.core.Fail(c, http.StatusConflict, err.Error())
		return
	}
	b.refundAudit(c, "payment.refund_created", r, "status", "amount_cents")
	b.core.OK(c, r)
}

// AdminListRefunds GET /admin/payment/refunds?status=&q=&page=&page_size= 退款与退款申请（待审核在前）。
func (b *behavior) AdminListRefunds(c *gin.Context) {
	page, pageSize := b.core.Paginate(c)
	db := b.core.Gorm()
	q := db.Model(&Refund{})
	if s := c.Query("status"); s != "" {
		q = q.Where("status = ?", s)
	}
	if kw := strings.TrimSpace(c.Query("q")); kw != "" {
		like := "%" + kw + "%"
		q = q.Where("refund_no LIKE ? OR order_no LIKE ? OR user_id IN (?)", like, like,
			db.Model(&models.User{}).Select("id").Where("username LIKE ? OR email LIKE ?", like, like))
	}
	var total int64
	q.Count(&total)
	var rows []Refund
	q.Order("CASE WHEN status = 'requested' THEN 0 WHEN status = 'processing' THEN 1 ELSE 2 END, id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows)
	userIDs, orderIDs := []uint{}, []uint{}
	for _, r := range rows {
		userIDs, orderIDs = append(userIDs, r.UserID), append(orderIDs, r.OrderID)
	}
	users := map[uint]userBrief{}
	orders := map[uint]Order{}
	if len(rows) > 0 {
		var ul []models.User
		db.Select("id, username, nickname, name_display, avatar").Where("id IN ?", userIDs).Find(&ul)
		for _, u := range ul {
			users[u.ID] = userBrief{ID: u.ID, Username: u.Username, Nickname: u.Nickname, DisplayName: u.PublicName(), Avatar: u.Avatar}
		}
		var ol []Order
		db.Where("id IN ?", orderIDs).Find(&ol)
		for _, o := range ol {
			orders[o.ID] = o
		}
	}
	var requested int64
	db.Model(&Refund{}).Where("status = ?", RefundRequested).Count(&requested)
	items := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		o := orders[r.OrderID]
		items = append(items, gin.H{"refund": r, "user": users[r.UserID], "order": gin.H{
			"order_no": o.OrderNo, "title": o.Title, "kind": o.Kind, "amount_cents": o.AmountCents, "currency": o.Currency,
			"channel": o.Channel, "paid_at": o.PaidAt, "refunded_cents": o.RefundedCents, "status": o.Status,
		}})
	}
	b.core.OK(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize, "requested": requested})
}

// AdminApproveRefund POST /admin/payment/refunds/:id/approve {amount_cents, revoke, note} 通过退款申请并向渠道发起。
func (b *behavior) AdminApproveRefund(c *gin.Context) {
	r, found := b.findRefund(c)
	if !found {
		return
	}
	var req struct {
		AmountCents int64  `json:"amount_cents"`
		Revoke      bool   `json:"revoke"`
		Note        string `json:"note"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.AmountCents == 0 {
		req.AmountCents = r.AmountCents
	}
	if err := b.approveRefund(c.Request.Context(), b.core.CurrentUser(c), r, req.AmountCents, req.Revoke, strings.TrimSpace(req.Note)); err != nil {
		b.core.Fail(c, http.StatusConflict, err.Error())
		return
	}
	b.refundAudit(c, "payment.refund_approved", r, "status", "amount_cents", "revoke")
	b.core.OK(c, r)
}

// AdminRejectRefund POST /admin/payment/refunds/:id/reject {note} 驳回退款申请。
func (b *behavior) AdminRejectRefund(c *gin.Context) {
	r, found := b.findRefund(c)
	if !found {
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.Note) == "" {
		b.core.Fail(c, http.StatusBadRequest, "请填写驳回理由")
		return
	}
	if err := b.rejectRefund(b.core.CurrentUser(c), r, strings.TrimSpace(req.Note)); err != nil {
		b.core.Fail(c, http.StatusConflict, err.Error())
		return
	}
	b.core.Gorm().First(r, r.ID)
	b.refundAudit(c, "payment.refund_rejected", r, "status")
	b.core.OK(c, r)
}

// AdminSyncRefund POST /admin/payment/refunds/:id/sync 立即向渠道查询受理中的退款。
func (b *behavior) AdminSyncRefund(c *gin.Context) {
	r, found := b.findRefund(c)
	if !found {
		return
	}
	r.SyncedAt = nil
	b.syncRefund(c.Request.Context(), r)
	b.core.OK(c, r)
}

// AdminResolveRefund POST /admin/payment/refunds/:id/resolve {succeeded, note} 人工确认受理中退款的结果（渠道无法给出结果时）。
func (b *behavior) AdminResolveRefund(c *gin.Context) {
	r, found := b.findRefund(c)
	if !found {
		return
	}
	var req struct {
		Succeeded bool   `json:"succeeded"`
		Note      string `json:"note"`
	}
	if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.Note) == "" {
		b.core.Fail(c, http.StatusBadRequest, "请填写核实说明")
		return
	}
	if err := b.resolveRefund(r, req.Succeeded, strings.TrimSpace(req.Note)); err != nil {
		b.core.Fail(c, http.StatusConflict, err.Error())
		return
	}
	b.core.Gorm().Model(&Refund{}).Where("id = ?", r.ID).Update("admin_note", truncateRunes(strings.TrimSpace(req.Note), 500))
	b.core.Gorm().First(r, r.ID)
	b.refundAudit(c, "payment.refund_resolved", r, "status")
	b.core.OK(c, r)
}

// AdminSettleRefund POST /admin/payment/refunds/:id/settle 重试退款成功后的商品回调（冲回财务/撤销商品）。
func (b *behavior) AdminSettleRefund(c *gin.Context) {
	r, found := b.findRefund(c)
	if !found {
		return
	}
	if r.Status != RefundSucceeded || r.SettledAt != nil {
		b.core.Fail(c, http.StatusConflict, "只有已退款且未完成商品回调的退款可以重试")
		return
	}
	var o Order
	b.core.Gorm().First(&o, r.OrderID)
	b.settle(r, &o)
	b.core.Gorm().First(r, r.ID)
	b.refundAudit(c, "payment.refund_settled", r, "settled_at")
	b.core.OK(c, r)
}

func (b *behavior) respondOrder(c *gin.Context, id uint) {
	var o Order
	b.core.Gorm().First(&o, id)
	b.core.OK(c, o)
}

func errOr(err error, fallback string) string {
	if err != nil {
		return err.Error()
	}
	return fallback
}

// AdminGetSettings GET /admin/payment/settings 各支付方式配置（密钥只返回是否已配置）与回调地址。
func (b *behavior) AdminGetSettings(c *gin.Context) {
	base := b.baseURL(c)
	out := adminView(b.core)
	out["notify_urls"] = gin.H{
		chAlipay: base + "/api/v1/payment/notify/alipay", chWechat: base + "/api/v1/payment/notify/wechat", chStripe: base + "/api/v1/payment/notify/stripe",
	}
	out["site_url_set"] = strings.TrimSpace(b.core.GetSetting("site_url")) != ""
	out["available"] = availableChannels(loadConfig(b.core), "")
	b.core.OK(c, out)
}

// AdminUpdateSettings PUT /admin/payment/settings 只保存传入的字段；密钥字段传空串表示不修改。
func (b *behavior) AdminUpdateSettings(c *gin.Context) {
	var req map[string]any
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	type write struct{ key, value, desc string }
	writes := []write{}
	changed := []string{}
	for _, s := range settings {
		raw, has := req[s.json]
		if !has {
			continue
		}
		var value string
		switch s.kind {
		case "bool":
			v, ok := raw.(bool)
			if !ok {
				b.core.Fail(c, http.StatusBadRequest, s.json+" 应为布尔值")
				return
			}
			value = strconv.FormatBool(v)
		case "int":
			v, ok := raw.(float64)
			min, max, msg := intRange(s.json)
			if !ok || v < float64(min) || v > float64(max) || v != float64(int(v)) {
				b.core.Fail(c, http.StatusBadRequest, msg)
				return
			}
			value = strconv.Itoa(int(v))
		default:
			v, ok := raw.(string)
			if !ok {
				b.core.Fail(c, http.StatusBadRequest, s.json+" 应为字符串")
				return
			}
			value = strings.TrimSpace(v)
			if s.secret && value == "" {
				continue
			}
			if err := validateSetting(s.json, value); err != nil {
				b.core.Fail(c, http.StatusBadRequest, err.Error())
				return
			}
		}
		writes = append(writes, write{s.key, value, s.desc})
		changed = append(changed, s.json)
	}
	for _, w := range writes {
		if err := b.core.SetSetting(w.key, w.value, w.desc); err != nil {
			b.core.Fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
	}
	b.core.RecordAudit(c, "payment.settings_updated", "payment", "settings", "支付设置", map[string]any{"changed_fields": changed})
	b.AdminGetSettings(c)
}

// validateSetting 校验密钥等配置的格式（保存前发现问题，避免下单时才报错）。
func validateSetting(field, value string) error {
	if value == "" {
		return nil
	}
	switch field {
	case "alipay_private_key", "wechat_private_key":
		if _, err := parsePrivateKey(value); err != nil {
			return fmt.Errorf("%s：%w", field, err)
		}
	case "alipay_public_key", "wechat_public_key":
		if _, err := parsePublicKey(value); err != nil {
			return fmt.Errorf("%s：%w", field, err)
		}
	case "wechat_api_v3_key":
		if len(value) != 32 {
			return errors.New("微信支付 APIv3 密钥应为 32 个字符")
		}
	case "offline_qr":
		if !strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
			return errors.New("收款码图片需为站内路径或 http(s) 地址")
		}
	case "offline_instructions":
		if len([]rune(value)) > 2000 {
			return errors.New("线下转账说明最多 2000 字")
		}
	}
	return nil
}
