package paidcontent

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

type behavior struct{ core plugincore.Core }

const (
	kindBook = "paid-book"
	kindDoc  = "paid-doc"
)

// —— 设置 ——

type settings struct {
	Currency          string `json:"currency"`
	CommissionPercent int    `json:"commission_percent"`   // 平台抽成比例（0–90）
	MinWithdrawal     int64  `json:"min_withdrawal_cents"` // 最低提现金额
	MaxPrice          int64  `json:"max_price_cents"`      // 单价上限
	AllowAuthors      bool   `json:"allow_authors"`        // 允许作者为自己的书定价（否则仅管理员可设置）
	UpgradeLink       string `json:"upgrade_link"`         // 付费墙上「升级免费读」的站内链接（如会员页），留空不显示
}

const (
	defaultCommission    = 20
	defaultMinWithdrawal = 10000
	defaultMaxPrice      = 1_000_000
)

func loadSettings(core plugincore.Core) settings {
	num := func(key string, def, min, max int64) int64 {
		v, err := strconv.ParseInt(strings.TrimSpace(core.GetSetting(key)), 10, 64)
		if err != nil || v < min || v > max {
			return def
		}
		return v
	}
	cur := strings.ToUpper(strings.TrimSpace(core.GetSetting("paid_currency")))
	if len(cur) != 3 {
		cur = "CNY"
	}
	return settings{
		Currency: cur, CommissionPercent: int(num("paid_commission_percent", defaultCommission, 0, 90)),
		MinWithdrawal: num("paid_min_withdrawal_cents", defaultMinWithdrawal, 1, 100_000_000),
		MaxPrice:      num("paid_max_price_cents", defaultMaxPrice, 1, 100_000_000),
		AllowAuthors:  core.GetSetting("paid_allow_authors") != "false",
		UpgradeLink:   strings.TrimSpace(core.GetSetting("paid_upgrade_link")),
	}
}

// —— 访问判定 ——

func enabled(core plugincore.Core) bool { return core.PluginEnabled(plugins.KeyPaidContent) }

func loadPaidBook(db *gorm.DB, bookID uint) (PaidBook, bool) {
	var pb PaidBook
	if db.First(&pb, bookID).Error != nil || !pb.Enabled {
		return pb, false
	}
	return pb, true
}

// readingOrder 书中已发布章节的阅读顺序（目录深度优先，按 sort_order、id）。
func readingOrder(db *gorm.DB, bookID uint) []uint {
	var docs []models.Document
	db.Select("id, parent_id, sort_order").Where("book_id = ? AND status = ?", bookID, "published").Find(&docs)
	children := map[uint][]models.Document{}
	for _, d := range docs {
		parent := uint(0)
		if d.ParentID != nil {
			parent = *d.ParentID
		}
		children[parent] = append(children[parent], d)
	}
	for k := range children {
		list := children[k]
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].SortOrder != list[j].SortOrder {
				return list[i].SortOrder < list[j].SortOrder
			}
			return list[i].ID < list[j].ID
		})
	}
	var out []uint
	var walk func(parent uint)
	walk = func(parent uint) {
		for _, d := range children[parent] {
			out = append(out, d.ID)
			walk(d.ID)
		}
	}
	walk(0)
	return out
}

// docPricing 章节的有效定价：是否免费、单独购买价（0 表示只能整本购买）。
func docPricing(db *gorm.DB, pb PaidBook, docID uint, order []uint) (free bool, price int64) {
	var pd PaidDoc
	if db.First(&pd, docID).Error == nil {
		if pd.Free {
			return true, 0
		}
		if pd.PriceCents > 0 {
			return false, pd.PriceCents
		}
	}
	if pb.FreeChapters > 0 {
		for i, id := range order {
			if i >= pb.FreeChapters {
				break
			}
			if id == docID {
				return true, 0
			}
		}
	}
	if pb.ChapterPriceCents == 0 && pb.BookPriceCents == 0 {
		return true, 0 // 未设置任何价格视为免费
	}
	return false, pb.ChapterPriceCents
}

// entitledFree 读者的权益是否让本书免费（全部免费，或内容访问等级达到书籍设定）。
func entitledFree(core plugincore.Core, u *models.User, pb PaidBook) bool {
	if u == nil {
		return false
	}
	ent := plugincore.ResolveEntitlements(core, u)
	if ent[entFreeAll].Value > 0 {
		return true
	}
	return pb.FreeTier > 0 && ent[entAccessTier].Value >= int64(pb.FreeTier)
}

func discountOf(core plugincore.Core, u *models.User) int64 {
	if u == nil {
		return 0
	}
	return plugincore.ResolveEntitlements(core, u)[entDiscount].Value
}

func discounted(price, percent int64) int64 {
	if percent <= 0 || price <= 0 {
		return price
	}
	if percent > maxDisc {
		percent = maxDisc
	}
	v := price * (100 - percent) / 100
	if v < 1 {
		v = 1
	}
	return v
}

func purchased(db *gorm.DB, userID, bookID, docID uint) bool {
	var n int64
	db.Model(&Purchase{}).Where("user_id = ? AND book_id = ? AND (doc_id = 0 OR doc_id = ?)", userID, bookID, docID).Count(&n)
	return n > 0
}

// makePreview 试读内容：按段落截取约 percent% 的正文；代码块未闭合时补齐围栏。
// makePreview 试读内容：按字数取正文的前 percent%（服务端截断，接口与页面都只拿到这部分）。
// 整段放得下的段落完整保留；放不下的段落在额度内截断，尽量停在句末标点，并以「…」结尾；
// 截断在代码块中时补齐结束标记，避免后续渲染错乱。整章只有一段时同样按比例截断。
func makePreview(content string, percent int) string {
	if percent <= 0 || content == "" {
		return ""
	}
	target := utf8.RuneCountInString(content) * percent / 100
	if target <= 0 {
		return ""
	}
	var b strings.Builder
	used := 0
	for i, p := range strings.Split(content, "\n\n") {
		sep := 0
		if i > 0 {
			sep = 2
		}
		runes := []rune(p)
		if used+sep+len(runes) <= target {
			if sep > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(p)
			used += sep + len(runes)
			continue
		}
		if remain := target - used - sep; remain > 0 {
			if sep > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(cutRunes(runes, remain))
			b.WriteString("…")
		}
		break
	}
	out := b.String()
	if strings.Count(out, "```")%2 == 1 {
		out += "\n```"
	}
	return out
}

// cutRunes 取前 n 个字符，尽量停在后半段的句末标点或换行处。
func cutRunes(r []rune, n int) string {
	if n >= len(r) {
		return string(r)
	}
	for i := n - 1; i >= n/2; i-- {
		switch r[i] {
		case '。', '！', '？', '；', '.', '!', '?', ';', '\n':
			return string(r[:i+1])
		}
	}
	return string(r[:n])
}

func gateDocument(core plugincore.Core, u *models.User, book *models.Book, doc *models.Document) plugincore.ContentAccess {
	if !enabled(core) {
		return plugincore.ContentAccess{Allowed: true}
	}
	db := core.Gorm()
	pb, paid := loadPaidBook(db, book.ID)
	if !paid {
		return plugincore.ContentAccess{Allowed: true}
	}
	free, price := docPricing(db, pb, doc.ID, orderIfNeeded(db, pb, book.ID))
	if free || entitledFree(core, u, pb) || (u != nil && purchased(db, u.ID, book.ID, doc.ID)) {
		return plugincore.ContentAccess{Allowed: true}
	}
	s := loadSettings(core)
	disc := discountOf(core, u)
	paywall := map[string]any{
		"book_id": book.ID, "doc_id": doc.ID, "currency": s.Currency, "discount_percent": disc,
		"book_price_cents": pb.BookPriceCents, "book_final_cents": discounted(pb.BookPriceCents, disc),
		"chapter_price_cents": price, "chapter_final_cents": discounted(price, disc),
		"free_tier": pb.FreeTier, "free_for": freeFor(core, pb), "logged_in": u != nil, "upgrade_link": s.UpgradeLink,
	}
	return plugincore.ContentAccess{Preview: makePreview(doc.Content, pb.PreviewPercent), Paywall: paywall}
}

// freeFor 可免费阅读本书的会员方案与成长等级（内容访问等级达到书的设置值）。
func freeFor(core plugincore.Core, pb PaidBook) []plugincore.EntitlementGrant {
	out := []plugincore.EntitlementGrant{}
	if pb.FreeTier <= 0 {
		return out
	}
	for _, g := range plugincore.EntitlementGrants(core, entAccessTier) {
		if g.Value == plugincore.Unlimited || g.Value >= int64(pb.FreeTier) {
			out = append(out, g)
		}
	}
	return out
}

func orderIfNeeded(db *gorm.DB, pb PaidBook, bookID uint) []uint {
	if pb.FreeChapters <= 0 {
		return nil
	}
	return readingOrder(db, bookID)
}

// lockedDocs 读者在本书中未解锁的已发布章节（整本已购/权益免费时为空）。
func lockedDocs(core plugincore.Core, u *models.User, book *models.Book, pb PaidBook) []uint {
	db := core.Gorm()
	if entitledFree(core, u, pb) || (u != nil && hasBookPurchase(db, u.ID, book.ID)) {
		return []uint{}
	}
	order := readingOrder(db, book.ID)
	var bought map[uint]bool
	if u != nil {
		var ids []uint
		db.Model(&Purchase{}).Where("user_id = ? AND book_id = ?", u.ID, book.ID).Pluck("doc_id", &ids)
		bought = map[uint]bool{}
		for _, id := range ids {
			bought[id] = true
		}
	}
	locked := []uint{}
	for _, id := range order {
		if free, _ := docPricing(db, pb, id, order); free || bought[id] {
			continue
		}
		locked = append(locked, id)
	}
	return locked
}

func hasBookPurchase(db *gorm.DB, userID, bookID uint) bool {
	var n int64
	db.Model(&Purchase{}).Where("user_id = ? AND book_id = ? AND doc_id = 0", userID, bookID).Count(&n)
	return n > 0
}

func gateBookFully(core plugincore.Core, u *models.User, book *models.Book) bool {
	if !enabled(core) {
		return true
	}
	pb, paid := loadPaidBook(core.Gorm(), book.ID)
	if !paid {
		return true
	}
	return len(lockedDocs(core, u, book, pb)) == 0
}

// —— 商品 ——

var errOwnBook = errors.New("这是你自己的书，无需购买")

func resolveBook(core plugincore.Core, u *models.User, sku string) (plugincore.Product, error) {
	if !enabled(core) {
		return plugincore.Product{}, errors.New("付费内容未启用")
	}
	id, _ := strconv.ParseUint(sku, 10, 64)
	db := core.Gorm()
	var book models.Book
	if id == 0 || db.First(&book, id).Error != nil {
		return plugincore.Product{}, errors.New("书籍不存在")
	}
	pb, paid := loadPaidBook(db, book.ID)
	if !paid || pb.BookPriceCents <= 0 {
		return plugincore.Product{}, errors.New("本书未开放整本购买")
	}
	if u != nil && u.ID == book.UserID {
		return plugincore.Product{}, errOwnBook
	}
	if u != nil && (hasBookPurchase(db, u.ID, book.ID) || entitledFree(core, u, pb)) {
		return plugincore.Product{}, errors.New("你已可以阅读全书，无需购买")
	}
	s := loadSettings(core)
	amount := discounted(pb.BookPriceCents, discountOf(core, u))
	return plugincore.Product{
		Kind: kindBook, SKU: sku, Title: book.Title, Description: "解锁全书", AmountCents: amount, Currency: s.Currency,
		ReturnLink: "/book/detail/" + book.Slug,
		Payload:    map[string]any{"book_id": book.ID, "doc_id": 0, "author_id": book.UserID, "amount_cents": amount, "currency": s.Currency, "commission": s.CommissionPercent, "title": book.Title},
	}, nil
}

func resolveDoc(core plugincore.Core, u *models.User, sku string) (plugincore.Product, error) {
	if !enabled(core) {
		return plugincore.Product{}, errors.New("付费内容未启用")
	}
	id, _ := strconv.ParseUint(sku, 10, 64)
	db := core.Gorm()
	var doc models.Document
	var book models.Book
	if id == 0 || db.First(&doc, id).Error != nil || db.First(&book, doc.BookID).Error != nil || doc.Status != "published" {
		return plugincore.Product{}, errors.New("章节不存在")
	}
	pb, paid := loadPaidBook(db, book.ID)
	if !paid {
		return plugincore.Product{}, errors.New("该章节免费")
	}
	free, price := docPricing(db, pb, doc.ID, orderIfNeeded(db, pb, book.ID))
	if free {
		return plugincore.Product{}, errors.New("该章节免费")
	}
	if price <= 0 {
		return plugincore.Product{}, errors.New("该章节需购买全书")
	}
	if u != nil && u.ID == book.UserID {
		return plugincore.Product{}, errOwnBook
	}
	if u != nil && (purchased(db, u.ID, book.ID, doc.ID) || entitledFree(core, u, pb)) {
		return plugincore.Product{}, errors.New("你已可以阅读该章节，无需购买")
	}
	s := loadSettings(core)
	amount := discounted(price, discountOf(core, u))
	title := book.Title + " · " + doc.Title
	return plugincore.Product{
		Kind: kindDoc, SKU: sku, Title: title, Description: "解锁章节", AmountCents: amount, Currency: s.Currency,
		ReturnLink: "/book/reader/" + book.Slug + "/" + doc.Slug,
		Payload:    map[string]any{"book_id": book.ID, "doc_id": doc.ID, "author_id": book.UserID, "amount_cents": amount, "currency": s.Currency, "commission": s.CommissionPercent, "title": title},
	}, nil
}

func payloadUint(v any) uint {
	switch n := v.(type) {
	case float64:
		return uint(n)
	case int:
		return uint(n)
	case uint:
		return n
	case int64:
		return uint(n)
	}
	return 0
}

func payloadInt(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	case uint:
		return int64(n)
	}
	return 0
}

// commissionOf 平台抽成（四舍五入到最小货币单位）。
func commissionOf(gross, percent int64) int64 { return (gross*percent + 50) / 100 }

// fulfill 支付成功：写入购买记录并为作者入账（按订单号幂等）。
func fulfill(core plugincore.Core, userID uint, orderNo string, payload map[string]any) error {
	bookID, docID, authorID := payloadUint(payload["book_id"]), payloadUint(payload["doc_id"]), payloadUint(payload["author_id"])
	amount, pct := payloadInt(payload["amount_cents"]), payloadInt(payload["commission"])
	currency, _ := payload["currency"].(string)
	title, _ := payload["title"].(string)
	if bookID == 0 || amount <= 0 {
		return fmt.Errorf("订单 %s 的商品快照无效", orderNo)
	}
	db := core.Gorm()
	var n int64
	db.Model(&Purchase{}).Where("order_no = ?", orderNo).Count(&n)
	if n > 0 {
		return nil
	}
	commission := commissionOf(amount, pct)
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&Purchase{UserID: userID, BookID: bookID, DocID: docID, OrderNo: orderNo, AmountCents: amount, Currency: currency, AuthorID: authorID}).Error; err != nil {
			return err
		}
		return tx.Create(&LedgerEntry{AuthorID: authorID, Kind: LedgerSale, OrderNo: orderNo, BookID: bookID, DocID: docID, Title: title, BuyerID: userID,
			GrossCents: amount, CommissionCents: commission, NetCents: amount - commission, Currency: currency}).Error
	})
	if err != nil {
		return err
	}
	var book models.Book
	db.Select("id, title").First(&book, bookID)
	core.NotifyI18n(authorID, "system", "notify.paid.sold", map[string]string{"book": book.Title, "title": title, "amount": formatMoney(amount-commission, currency)},
		map[string]any{"link": "/user/earnings"})
	return nil
}

// refund 订单退款：按比例扣回作者净收益（平台抽成同比例退回）；选择撤销时收回解锁（删除购买记录）。
// 按退款单号幂等；订单从未履约（没有售出流水）时无需处理。
func refund(core plugincore.Core, ev plugincore.RefundEvent) error {
	db := core.Gorm()
	var n int64
	db.Model(&LedgerEntry{}).Where("kind = ? AND refund_no = ?", LedgerRefund, ev.RefundNo).Count(&n)
	var sale LedgerEntry
	if n > 0 || db.Where("kind = ? AND order_no = ?", LedgerSale, ev.OrderNo).First(&sale).Error != nil || sale.GrossCents <= 0 {
		return nil
	}
	amount := ev.AmountCents
	if amount > sale.GrossCents {
		amount = sale.GrossCents
	}
	netBack := int64(math.Round(float64(sale.NetCents) * float64(amount) / float64(sale.GrossCents)))
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&LedgerEntry{AuthorID: sale.AuthorID, Kind: LedgerRefund, OrderNo: ev.OrderNo, RefundNo: ev.RefundNo, BookID: sale.BookID,
			DocID: sale.DocID, Title: sale.Title, BuyerID: sale.BuyerID, GrossCents: -amount, CommissionCents: -(amount - netBack), NetCents: -netBack,
			Currency: sale.Currency}).Error; err != nil {
			return err
		}
		if ev.Revoke {
			return tx.Where("order_no = ?", ev.OrderNo).Delete(&Purchase{}).Error
		}
		return nil
	})
	if err != nil {
		return err
	}
	var book models.Book
	db.Select("id, title").First(&book, sale.BookID)
	core.NotifyI18n(sale.AuthorID, "system", "notify.paid.refunded", map[string]string{"book": book.Title, "title": sale.Title, "amount": formatMoney(netBack, sale.Currency)},
		map[string]any{"link": "/user/earnings"})
	return nil
}

func formatMoney(cents int64, currency string) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return fmt.Sprintf("%s%d.%02d %s", sign, cents/100, cents%100, currency)
}

// balanceOf 作者可提现余额（流水净额之和）。
func balanceOf(db *gorm.DB, authorID uint) int64 {
	var sum struct{ Total int64 }
	db.Model(&LedgerEntry{}).Select("COALESCE(SUM(net_cents), 0) AS total").Where("author_id = ?", authorID).Scan(&sum)
	return sum.Total
}
