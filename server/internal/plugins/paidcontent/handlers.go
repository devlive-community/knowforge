package paidcontent

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

func (b *behavior) Key() string { return plugins.KeyPaidContent }

func (b *behavior) RegisterRoutes(api *gin.RouterGroup, core plugincore.Core) {
	b.core = core
	feat := core.RequireFeaturePlugin(plugins.KeyPaidContent)
	api.GET("/paid/books/:id", core.OptionalAuth(), feat, b.BookInfo)
	user := []gin.HandlerFunc{core.RequireAuth(), feat, core.RequirePermissionMiddleware(PermUse)}
	with := func(h gin.HandlerFunc) []gin.HandlerFunc { return append(append([]gin.HandlerFunc{}, user...), h) }
	api.GET("/books/:id/paid-settings", with(b.GetBookSettings)...)
	api.PUT("/books/:id/paid-settings", with(b.UpdateBookSettings)...)
	api.GET("/users/me/purchases", with(b.MyPurchases)...)
	api.GET("/users/me/earnings", with(b.MyEarnings)...)
	api.POST("/users/me/withdrawals", with(b.RequestWithdrawal)...)
	admin := []gin.HandlerFunc{core.RequireAuth(), core.RequireAdmin(), feat, core.RequirePermissionMiddleware(PermManage)}
	reg := func(method, path string, h gin.HandlerFunc) {
		api.Handle(method, path, append(append([]gin.HandlerFunc{}, admin...), h)...)
	}
	reg(http.MethodGet, "/admin/paid/sales", b.AdminSales)
	reg(http.MethodGet, "/admin/paid/withdrawals", b.AdminWithdrawals)
	reg(http.MethodPost, "/admin/paid/withdrawals/:id/pay", b.AdminPayWithdrawal)
	reg(http.MethodPost, "/admin/paid/withdrawals/:id/reject", b.AdminRejectWithdrawal)
	reg(http.MethodGet, "/admin/paid/settings", b.AdminGetSettings)
	reg(http.MethodPut, "/admin/paid/settings", b.AdminUpdateSettings)
}

func changedFields(fields ...string) map[string]any { return map[string]any{"changed_fields": fields} }

type userBrief struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
}

func (b *behavior) userBriefs(ids []uint) map[uint]userBrief {
	out := map[uint]userBrief{}
	if len(ids) == 0 {
		return out
	}
	var users []models.User
	b.core.Gorm().Select("id, username, nickname, avatar").Where("id IN ?", ids).Find(&users)
	for _, u := range users {
		out[u.ID] = userBrief{ID: u.ID, Username: u.Username, Nickname: u.Nickname, Avatar: u.Avatar}
	}
	return out
}

// BookInfo GET /paid/books/:id 书籍付费信息与当前读者的访问状态（详情页、阅读页目录用）。
func (b *behavior) BookInfo(c *gin.Context) {
	book, _ := b.core.FindBook(c)
	u := b.core.CurrentUser(c)
	if book == nil || !b.core.CanReadBook(u, book) {
		b.core.Fail(c, http.StatusNotFound, "书籍不存在")
		return
	}
	pb, paid := loadPaidBook(b.core.Gorm(), book.ID)
	if !paid {
		b.core.OK(c, gin.H{"enabled": false})
		return
	}
	s := loadSettings(b.core)
	disc := discountOf(b.core, u)
	locked := []uint{}
	editor := u != nil && (b.core.IsAdmin(u) || b.core.CanEditBookContent(u, book))
	if !editor {
		locked = lockedDocs(b.core, u, book, pb)
	}
	purchasedBook := u != nil && hasBookPurchase(b.core.Gorm(), u.ID, book.ID)
	b.core.OK(c, gin.H{
		"enabled": true, "currency": s.Currency, "book_price_cents": pb.BookPriceCents, "book_final_cents": discounted(pb.BookPriceCents, disc),
		"chapter_price_cents": pb.ChapterPriceCents, "free_chapters": pb.FreeChapters, "preview_percent": pb.PreviewPercent,
		"free_tier": pb.FreeTier, "free_for": freeFor(b.core, pb), "discount_percent": disc, "purchased_book": purchasedBook, "can_read_all": len(locked) == 0,
		"locked_doc_ids": locked, "upgrade_link": s.UpgradeLink, "is_author": editor,
	})
}

// canPrice 能否为书定价：书籍作者（站点允许作者定价时）或管理员。
func (b *behavior) canPrice(c *gin.Context) (*models.Book, bool) {
	book, status := b.core.FindBook(c)
	if book == nil {
		b.core.Fail(c, status, "书籍不存在")
		return nil, false
	}
	u := b.core.CurrentUser(c)
	if b.core.IsAdmin(u) {
		return book, true
	}
	if u.ID != book.UserID {
		b.core.Fail(c, http.StatusForbidden, "只有书籍作者可以设置付费")
		return nil, false
	}
	if !loadSettings(b.core).AllowAuthors {
		b.core.Fail(c, http.StatusForbidden, "站点暂未开放作者定价")
		return nil, false
	}
	return book, true
}

type docSetting struct {
	DocID      uint  `json:"doc_id"`
	Free       bool  `json:"free"`
	PriceCents int64 `json:"price_cents"`
}

// GetBookSettings GET /books/:id/paid-settings 书籍付费设置与各章节（含单独设置与生效结果）。
func (b *behavior) GetBookSettings(c *gin.Context) {
	book, allowed := b.canPrice(c)
	if !allowed {
		return
	}
	db := b.core.Gorm()
	var pb PaidBook
	if db.First(&pb, book.ID).Error != nil {
		pb = PaidBook{BookID: book.ID, PreviewPercent: 10}
	}
	var overrides []PaidDoc
	db.Where("book_id = ?", book.ID).Find(&overrides)
	byDoc := map[uint]PaidDoc{}
	for _, o := range overrides {
		byDoc[o.DocID] = o
	}
	order := readingOrder(db, book.ID)
	var docs []models.Document
	db.Select("id, title, parent_id, status").Where("book_id = ? AND status = ?", book.ID, "published").Find(&docs)
	titles := map[uint]models.Document{}
	for _, d := range docs {
		titles[d.ID] = d
	}
	items := make([]gin.H, 0, len(order))
	for i, id := range order {
		d := titles[id]
		free, price := docPricing(db, pb, id, order)
		o := byDoc[id]
		items = append(items, gin.H{"doc_id": id, "title": d.Title, "parent_id": d.ParentID, "index": i, "free": o.Free, "price_cents": o.PriceCents,
			"effective_free": free, "effective_price_cents": price})
	}
	s := loadSettings(b.core)
	b.core.OK(c, gin.H{"settings": pb, "docs": items, "currency": s.Currency, "max_price_cents": s.MaxPrice, "commission_percent": s.CommissionPercent,
		"tier_grants": plugincore.EntitlementGrants(b.core, entAccessTier)}) // 「哪些会员 / 等级免费阅读」的选项
}

// UpdateBookSettings PUT /books/:id/paid-settings 保存付费设置（章节单独设置整体替换）。
func (b *behavior) UpdateBookSettings(c *gin.Context) {
	book, allowed := b.canPrice(c)
	if !allowed {
		return
	}
	var req struct {
		Enabled           bool         `json:"enabled"`
		BookPriceCents    int64        `json:"book_price_cents"`
		ChapterPriceCents int64        `json:"chapter_price_cents"`
		FreeChapters      int          `json:"free_chapters"`
		PreviewPercent    int          `json:"preview_percent"`
		FreeTier          int          `json:"free_tier"`
		Docs              []docSetting `json:"docs"`
	}
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	s := loadSettings(b.core)
	inRange := func(v int64) bool { return v >= 0 && v <= s.MaxPrice }
	switch {
	case !inRange(req.BookPriceCents) || !inRange(req.ChapterPriceCents):
		b.core.Fail(c, http.StatusBadRequest, fmt.Sprintf("价格需在 0 到 %s 之间", formatMoney(s.MaxPrice, s.Currency)))
		return
	case req.FreeChapters < 0 || req.FreeChapters > 10000:
		b.core.Fail(c, http.StatusBadRequest, "免费章节数无效")
		return
	case req.PreviewPercent < 0 || req.PreviewPercent > 50:
		b.core.Fail(c, http.StatusBadRequest, "试读比例需在 0 到 50 之间")
		return
	case req.FreeTier < 0 || req.FreeTier > maxTier:
		b.core.Fail(c, http.StatusBadRequest, fmt.Sprintf("免费等级需在 0 到 %d 之间", maxTier))
		return
	}
	db := b.core.Gorm()
	var owned []uint
	db.Model(&models.Document{}).Where("book_id = ?", book.ID).Pluck("id", &owned)
	ownedSet := map[uint]bool{}
	for _, id := range owned {
		ownedSet[id] = true
	}
	docs := []PaidDoc{}
	anyDocPrice := false
	for _, d := range req.Docs {
		if !ownedSet[d.DocID] {
			b.core.Fail(c, http.StatusBadRequest, "章节不属于本书")
			return
		}
		if !inRange(d.PriceCents) {
			b.core.Fail(c, http.StatusBadRequest, "章节价格无效")
			return
		}
		if !d.Free && d.PriceCents == 0 {
			continue
		}
		anyDocPrice = anyDocPrice || d.PriceCents > 0
		docs = append(docs, PaidDoc{DocID: d.DocID, BookID: book.ID, Free: d.Free, PriceCents: d.PriceCents})
	}
	if req.Enabled && req.BookPriceCents == 0 && req.ChapterPriceCents == 0 && !anyDocPrice {
		b.core.Fail(c, http.StatusBadRequest, "开启付费需至少设置整本价格或章节价格")
		return
	}
	pb := PaidBook{BookID: book.ID, Enabled: req.Enabled, BookPriceCents: req.BookPriceCents, ChapterPriceCents: req.ChapterPriceCents,
		FreeChapters: req.FreeChapters, PreviewPercent: req.PreviewPercent, FreeTier: req.FreeTier, UpdatedAt: time.Now()}
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&pb).Error; err != nil {
			return err
		}
		if err := tx.Where("book_id = ?", book.ID).Delete(&PaidDoc{}).Error; err != nil {
			return err
		}
		if len(docs) > 0 {
			return tx.Create(&docs).Error
		}
		return nil
	})
	if err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}
	b.GetBookSettings(c)
}

// MyPurchases GET /users/me/purchases?page= 我购买的书籍与章节。
func (b *behavior) MyPurchases(c *gin.Context) {
	page, pageSize := b.core.Paginate(c)
	db := b.core.Gorm()
	q := db.Model(&Purchase{}).Where("user_id = ?", b.core.CurrentUser(c).ID)
	var total int64
	q.Count(&total)
	var rows []Purchase
	q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows)
	bookIDs, docIDs := []uint{}, []uint{}
	for _, r := range rows {
		bookIDs = append(bookIDs, r.BookID)
		if r.DocID != 0 {
			docIDs = append(docIDs, r.DocID)
		}
	}
	books, docs := map[uint]models.Book{}, map[uint]models.Document{}
	var bl []models.Book
	db.Select("id, title, slug, cover_image").Where("id IN ?", bookIDs).Find(&bl)
	for _, bk := range bl {
		books[bk.ID] = bk
	}
	var dl []models.Document
	db.Select("id, title, slug").Where("id IN ?", docIDs).Find(&dl)
	for _, d := range dl {
		docs[d.ID] = d
	}
	items := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		item := gin.H{"purchase": r, "book": gin.H{"id": r.BookID, "title": books[r.BookID].Title, "slug": books[r.BookID].Slug, "cover_image": books[r.BookID].CoverImage}}
		if d, ok := docs[r.DocID]; ok {
			item["doc"] = gin.H{"id": d.ID, "title": d.Title, "slug": d.Slug}
		}
		items = append(items, item)
	}
	b.core.OK(c, plugincore.PageResult{Items: items, Total: total, Page: page, PageSize: pageSize})
}

// MyEarnings GET /users/me/earnings?page= 作者收益：余额、累计、流水与提现记录。
func (b *behavior) MyEarnings(c *gin.Context) {
	page, pageSize := b.core.Paginate(c)
	u := b.core.CurrentUser(c)
	db := b.core.Gorm()
	var totals struct {
		Gross      int64
		Commission int64
		Net        int64
	}
	db.Model(&LedgerEntry{}).Select("COALESCE(SUM(gross_cents),0) AS gross, COALESCE(SUM(commission_cents),0) AS commission, COALESCE(SUM(net_cents),0) AS net").
		Where("author_id = ? AND kind = ?", u.ID, LedgerSale).Scan(&totals)
	q := db.Model(&LedgerEntry{}).Where("author_id = ?", u.ID)
	var total int64
	q.Count(&total)
	var ledger []LedgerEntry
	q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&ledger)
	var withdrawals []Withdrawal
	db.Where("author_id = ?", u.ID).Order("id DESC").Limit(20).Find(&withdrawals)
	s := loadSettings(b.core)
	b.core.OK(c, gin.H{
		"balance_cents": balanceOf(db, u.ID), "currency": s.Currency, "min_withdrawal_cents": s.MinWithdrawal, "commission_percent": s.CommissionPercent,
		"total_gross_cents": totals.Gross, "total_commission_cents": totals.Commission, "total_net_cents": totals.Net,
		"ledger": plugincore.PageResult{Items: ledger, Total: total, Page: page, PageSize: pageSize}, "withdrawals": withdrawals,
	})
}

// RequestWithdrawal POST /users/me/withdrawals {amount_cents, account} 申请提现（同一时间只能有一笔待处理）。
func (b *behavior) RequestWithdrawal(c *gin.Context) {
	var req struct {
		AmountCents int64  `json:"amount_cents"`
		Account     string `json:"account"`
	}
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	account := strings.TrimSpace(req.Account)
	if account == "" || utf8.RuneCountInString(account) > 500 {
		b.core.Fail(c, http.StatusBadRequest, "请填写收款信息（不超过 500 字）")
		return
	}
	u := b.core.CurrentUser(c)
	s := loadSettings(b.core)
	if req.AmountCents < s.MinWithdrawal {
		b.core.Fail(c, http.StatusBadRequest, "提现金额不能低于 "+formatMoney(s.MinWithdrawal, s.Currency))
		return
	}
	db := b.core.Gorm()
	var w Withdrawal
	err := db.Transaction(func(tx *gorm.DB) error {
		var pending int64
		tx.Model(&Withdrawal{}).Where("author_id = ? AND status = ?", u.ID, WithdrawalPending).Count(&pending)
		if pending > 0 {
			return fmt.Errorf("已有一笔提现正在处理中")
		}
		if req.AmountCents > balanceOf(tx, u.ID) {
			return fmt.Errorf("可提现余额不足")
		}
		w = Withdrawal{AuthorID: u.ID, AmountCents: req.AmountCents, Currency: s.Currency, Account: account, Status: WithdrawalPending}
		if err := tx.Create(&w).Error; err != nil {
			return err
		}
		return tx.Create(&LedgerEntry{AuthorID: u.ID, Kind: LedgerWithdrawal, NetCents: -req.AmountCents, Currency: s.Currency, WithdrawalID: w.ID, Title: "提现申请"}).Error
	})
	if err != nil {
		b.core.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	b.core.OK(c, w)
}

// —— 管理端 ——

// AdminSales GET /admin/paid/sales?page= 销售流水（含作者、买家）与合计。
func (b *behavior) AdminSales(c *gin.Context) {
	page, pageSize := b.core.Paginate(c)
	db := b.core.Gorm()
	q := db.Model(&LedgerEntry{}).Where("kind = ?", LedgerSale)
	var total int64
	q.Count(&total)
	var rows []LedgerEntry
	q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows)
	var sums struct {
		Gross      int64
		Commission int64
	}
	db.Model(&LedgerEntry{}).Select("COALESCE(SUM(gross_cents),0) AS gross, COALESCE(SUM(commission_cents),0) AS commission").Where("kind = ?", LedgerSale).Scan(&sums)
	ids := []uint{}
	for _, r := range rows {
		ids = append(ids, r.AuthorID, r.BuyerID)
	}
	users := b.userBriefs(ids)
	items := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		items = append(items, gin.H{"entry": r, "author": users[r.AuthorID], "buyer": users[r.BuyerID]})
	}
	b.core.OK(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize, "gross_cents": sums.Gross, "commission_cents": sums.Commission, "currency": loadSettings(b.core).Currency})
}

// AdminWithdrawals GET /admin/paid/withdrawals?status=&page=
func (b *behavior) AdminWithdrawals(c *gin.Context) {
	page, pageSize := b.core.Paginate(c)
	q := b.core.Gorm().Model(&Withdrawal{})
	if s := c.Query("status"); s == WithdrawalPending || s == WithdrawalPaid || s == WithdrawalRejected {
		q = q.Where("status = ?", s)
	}
	var total int64
	q.Count(&total)
	var rows []Withdrawal
	q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows)
	ids := []uint{}
	for _, r := range rows {
		ids = append(ids, r.AuthorID)
	}
	users := b.userBriefs(ids)
	items := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		items = append(items, gin.H{"withdrawal": r, "author": users[r.AuthorID], "balance_cents": balanceOf(b.core.Gorm(), r.AuthorID)})
	}
	b.core.OK(c, plugincore.PageResult{Items: items, Total: total, Page: page, PageSize: pageSize})
}

func (b *behavior) processWithdrawal(c *gin.Context, pay bool) {
	var req struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&req)
	note := strings.TrimSpace(req.Note)
	if !pay && note == "" {
		b.core.Fail(c, http.StatusBadRequest, "请填写驳回原因")
		return
	}
	if utf8.RuneCountInString(note) > 500 {
		b.core.Fail(c, http.StatusBadRequest, "备注不能超过 500 字")
		return
	}
	db := b.core.Gorm()
	var w Withdrawal
	if db.First(&w, c.Param("id")).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "提现申请不存在")
		return
	}
	admin := b.core.CurrentUser(c)
	now := time.Now()
	status := WithdrawalPaid
	if !pay {
		status = WithdrawalRejected
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&Withdrawal{}).Where("id = ? AND status = ?", w.ID, WithdrawalPending).
			Updates(map[string]any{"status": status, "admin_note": note, "processed_by": admin.ID, "processed_at": &now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return fmt.Errorf("该申请已处理")
		}
		if !pay { // 驳回：冻结金额退回余额
			return tx.Create(&LedgerEntry{AuthorID: w.AuthorID, Kind: LedgerWithdrawalRevert, NetCents: w.AmountCents, Currency: w.Currency, WithdrawalID: w.ID, Title: "提现退回"}).Error
		}
		return nil
	})
	if err != nil {
		b.core.Fail(c, http.StatusConflict, err.Error())
		return
	}
	amount := formatMoney(w.AmountCents, w.Currency)
	id := strconv.FormatUint(uint64(w.ID), 10)
	if pay {
		b.core.NotifyI18n(w.AuthorID, "system", "notify.paid.withdrawalPaid", map[string]string{"amount": amount}, map[string]any{"link": "/user/earnings"})
		b.core.RecordAudit(c, "paid.withdrawal_paid", "paid_withdrawal", id, amount, changedFields("status"))
	} else {
		b.core.NotifyI18n(w.AuthorID, "system", "notify.paid.withdrawalRejected", map[string]string{"amount": amount, "note": note}, map[string]any{"link": "/user/earnings"})
		b.core.RecordAudit(c, "paid.withdrawal_rejected", "paid_withdrawal", id, amount, changedFields("status"))
	}
	db.First(&w, w.ID)
	b.core.OK(c, w)
}

// AdminPayWithdrawal POST /admin/paid/withdrawals/:id/pay {note?} 确认已线下打款。
func (b *behavior) AdminPayWithdrawal(c *gin.Context) { b.processWithdrawal(c, true) }

// AdminRejectWithdrawal POST /admin/paid/withdrawals/:id/reject {note} 驳回（金额退回作者余额）。
func (b *behavior) AdminRejectWithdrawal(c *gin.Context) { b.processWithdrawal(c, false) }

// AdminGetSettings GET /admin/paid/settings
func (b *behavior) AdminGetSettings(c *gin.Context) { b.core.OK(c, loadSettings(b.core)) }

// AdminUpdateSettings PUT /admin/paid/settings（可只传部分字段）
func (b *behavior) AdminUpdateSettings(c *gin.Context) {
	var req struct {
		Currency          *string `json:"currency"`
		CommissionPercent *int    `json:"commission_percent"`
		MinWithdrawal     *int64  `json:"min_withdrawal_cents"`
		MaxPrice          *int64  `json:"max_price_cents"`
		AllowAuthors      *bool   `json:"allow_authors"`
		UpgradeLink       *string `json:"upgrade_link"`
	}
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	type write struct{ key, value, desc, field string }
	writes := []write{}
	if req.Currency != nil {
		v := strings.ToUpper(strings.TrimSpace(*req.Currency))
		if len(v) != 3 {
			b.core.Fail(c, http.StatusBadRequest, "货币需为 3 位字母代码")
			return
		}
		writes = append(writes, write{"paid_currency", v, "付费内容：价格货币", "currency"})
	}
	if req.CommissionPercent != nil {
		if *req.CommissionPercent < 0 || *req.CommissionPercent > 90 {
			b.core.Fail(c, http.StatusBadRequest, "抽成比例需在 0 到 90 之间")
			return
		}
		writes = append(writes, write{"paid_commission_percent", strconv.Itoa(*req.CommissionPercent), "付费内容：平台抽成比例（%）", "commission_percent"})
	}
	if req.MinWithdrawal != nil {
		if *req.MinWithdrawal < 1 || *req.MinWithdrawal > 100_000_000 {
			b.core.Fail(c, http.StatusBadRequest, "最低提现金额无效")
			return
		}
		writes = append(writes, write{"paid_min_withdrawal_cents", strconv.FormatInt(*req.MinWithdrawal, 10), "付费内容：最低提现金额（分）", "min_withdrawal_cents"})
	}
	if req.MaxPrice != nil {
		if *req.MaxPrice < 1 || *req.MaxPrice > 100_000_000 {
			b.core.Fail(c, http.StatusBadRequest, "单价上限无效")
			return
		}
		writes = append(writes, write{"paid_max_price_cents", strconv.FormatInt(*req.MaxPrice, 10), "付费内容：单价上限（分）", "max_price_cents"})
	}
	if req.AllowAuthors != nil {
		writes = append(writes, write{"paid_allow_authors", strconv.FormatBool(*req.AllowAuthors), "付费内容：允许作者定价", "allow_authors"})
	}
	if req.UpgradeLink != nil {
		v := strings.TrimSpace(*req.UpgradeLink)
		if v != "" && !strings.HasPrefix(v, "/") {
			b.core.Fail(c, http.StatusBadRequest, "升级链接需为站内路径（以 / 开头）")
			return
		}
		writes = append(writes, write{"paid_upgrade_link", v, "付费内容：付费墙「升级免费读」链接", "upgrade_link"})
	}
	fields := []string{}
	for _, w := range writes {
		if err := b.core.SetSetting(w.key, w.value, w.desc); err != nil {
			b.core.Fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
		fields = append(fields, w.field)
	}
	b.core.RecordAudit(c, "paid.settings_updated", "paid", "settings", "付费内容设置", changedFields(fields...))
	b.AdminGetSettings(c)
}
