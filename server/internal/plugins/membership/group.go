package membership

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// —— 团队会员：为成员组（plugincore.MemberGroupProvider，如团队空间的团队）按席位购买会员。——
// 组内按席位优先次序（如所有者、管理员、再按加入时间）的前 N 名成员享有方案权益；个人会员与团队会员同时有效时，
// 每项权益取两者中较高的值。购买与续期：每个席位按方案该档价格计价，有效期内只能按当前方案与席位数续期（顺延）；
// 增加席位按剩余天数折算（以开通时每席每天的单价计）。只有能管理该组的人（如团队所有者与管理员）可以购买。

const (
	groupProductKind = "membership_group"       // SKU：价格ID-组类型-组ID-席位数
	seatsProductKind = "membership_group_seats" // SKU：组类型-组ID-增加的席位数
	maxGroupSeats    = 1000
	groupReturnLink  = "/user/membership/teams"
)

var errGroupForbidden = errors.New("只有团队所有者和管理员可以购买团队会员")

// GroupMembership 成员组的会员（每组一条）。
type GroupMembership struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	GroupKind string    `gorm:"size:32;uniqueIndex:uk_member_group;not null" json:"group_kind"`
	GroupID   uint      `gorm:"uniqueIndex:uk_member_group;not null" json:"group_id"`
	PlanID    uint      `gorm:"index;not null" json:"plan_id"`
	Seats     int       `gorm:"not null" json:"seats"`
	StartedAt time.Time `json:"started_at"`
	ExpiresAt time.Time `gorm:"index" json:"expires_at"`
	// SeatMicrosPerDay 开通时每个席位每天的价格（最小货币单位的百万分之一），用于折算增加席位的价格
	SeatMicrosPerDay int64      `json:"-"`
	BuyerID          uint       `json:"buyer_id"`
	RemindedAt       *time.Time `json:"-"`
	ExpiredNoticeAt  *time.Time `json:"-"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

func (GroupMembership) TableName() string { return "membership_groups" }

// GroupRecord 团队会员流水。
type GroupRecord struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	GroupKind     string     `gorm:"size:32;index:idx_member_group_record" json:"group_kind"`
	GroupID       uint       `gorm:"index:idx_member_group_record" json:"group_id"`
	PlanID        uint       `json:"plan_id"`
	PlanName      string     `gorm:"size:120" json:"plan_name"`
	Action        string     `gorm:"size:20" json:"action"` // grant | extend | switch | seats | adjust | revoke
	Days          int        `json:"days"`
	Seats         int        `json:"seats"` // 变动后的席位数
	PrevExpiresAt *time.Time `json:"prev_expires_at"`
	ExpiresAt     *time.Time `json:"expires_at"`
	Source        string     `gorm:"size:20;index:idx_member_group_source" json:"source"`
	SourceRef     string     `gorm:"size:64;index:idx_member_group_source" json:"source_ref"`
	OperatorID    uint       `json:"operator_id"`
	Reason        string     `gorm:"size:255" json:"reason"`
	CreatedAt     time.Time  `json:"created_at"`
}

func (GroupRecord) TableName() string { return "membership_group_records" }

// ActionSeats 增加（或退款扣回）席位。
const ActionSeats = "seats"

func init() {
	plugincore.RegisterProductProvider(plugincore.ProductProvider{Kind: groupProductKind, Resolve: resolveGroupProduct, Fulfill: fulfillGroupOrder, Refund: refundGroupOrder})
	plugincore.RegisterProductProvider(plugincore.ProductProvider{Kind: seatsProductKind, Resolve: resolveSeatsProduct, Fulfill: fulfillSeatsOrder, Refund: refundSeatsOrder})
}

// —— 组与覆盖 ——

func loadGroup(core plugincore.Core, viewer *models.User, kind string, id uint) (plugincore.MemberGroupProvider, plugincore.MemberGroup, bool) {
	p, ok := plugincore.MemberGroupProviderFor(core, kind)
	if !ok {
		return p, plugincore.MemberGroup{}, false
	}
	g, ok := p.Get(core, viewer, id)
	return p, g, ok
}

// groupCoverage 用户所在组中有效的团队会员，covered 表示该用户在席位内。
type groupCoverage struct {
	Sub     GroupMembership
	Covered bool
}

func userGroupCoverage(core plugincore.Core, userID uint, now time.Time) []groupCoverage {
	out := []groupCoverage{}
	db := core.Gorm()
	for _, p := range plugincore.MemberGroupProviders(core) {
		ids := p.GroupsOf(core, userID)
		if len(ids) == 0 {
			continue
		}
		var subs []GroupMembership
		db.Where("group_kind = ? AND group_id IN ? AND expires_at > ?", p.Kind, ids, now).Find(&subs)
		for _, s := range subs {
			members := p.Members(core, s.GroupID)
			covered := false
			for i, id := range members {
				if i >= s.Seats {
					break
				}
				if id == userID {
					covered = true
					break
				}
			}
			out = append(out, groupCoverage{Sub: s, Covered: covered})
		}
	}
	return out
}

// mergeEntitlements 两组权益逐项取较高值（不限 = -1 视为最大）。
func mergeEntitlements(dst map[string]int64, src models.EntitlementMap) {
	for k, v := range src {
		cur, has := dst[k]
		switch {
		case !has, v == plugincore.Unlimited:
			dst[k] = v
		case cur == plugincore.Unlimited:
		case v > cur:
			dst[k] = v
		}
	}
}

// —— 商品 ——

func parseGroupSKU(sku string, parts int) ([]string, bool) {
	s := strings.Split(sku, "-")
	return s, len(s) == parts
}

func atoiUint(s string) uint {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return uint(n)
}

func activeGroupSub(db *gorm.DB, kind string, id uint, now time.Time) (*GroupMembership, bool) {
	var sub GroupMembership
	if db.Where("group_kind = ? AND group_id = ?", kind, id).First(&sub).Error != nil {
		return nil, false
	}
	return &sub, sub.ExpiresAt.After(now)
}

// resolveGroupProduct 团队会员（开通或续期）：SKU = 价格ID-组类型-组ID-席位数。
func resolveGroupProduct(core plugincore.Core, u *models.User, sku string) (plugincore.Product, error) {
	if !core.PluginEnabled(plugins.KeyMembership) {
		return plugincore.Product{}, errors.New("会员功能未启用")
	}
	parts, valid := parseGroupSKU(sku, 4)
	if !valid {
		return plugincore.Product{}, errors.New("商品不存在")
	}
	priceID, kind, groupID, seats := atoiUint(parts[0]), parts[1], atoiUint(parts[2]), int(atoiUint(parts[3]))
	db := core.Gorm()
	var price Price
	var plan Plan
	if db.First(&price, priceID).Error != nil || db.First(&plan, price.PlanID).Error != nil {
		return plugincore.Product{}, errors.New("商品不存在")
	}
	if plan.Status != "active" {
		return plugincore.Product{}, errPlanArchived
	}
	if !plan.GroupEnabled {
		return plugincore.Product{}, errors.New("该方案不支持团队购买")
	}
	_, g, found := loadGroup(core, u, kind, groupID)
	if !found {
		return plugincore.Product{}, errors.New("团队不存在")
	}
	if !g.CanPurchase {
		return plugincore.Product{}, errGroupForbidden
	}
	if seats < 1 || seats > maxGroupSeats {
		return plugincore.Product{}, fmt.Errorf("席位数需在 1 到 %d 之间", maxGroupSeats)
	}
	if sub, active := activeGroupSub(db, kind, groupID, time.Now()); active && (sub.PlanID != plan.ID || sub.Seats != seats) {
		return plugincore.Product{}, errors.New("团队会员有效期内只能按当前方案与席位数续期；需要更多席位请使用「增加席位」")
	}
	b := &behavior{core: core}
	return plugincore.Product{
		Kind: groupProductKind, SKU: sku, Title: fmt.Sprintf("%s · %s（%d 席）", plan.Name, g.Name, seats), Description: plan.Description,
		DurationDays: price.DurationDays, AmountCents: price.PriceCents * int64(seats), Currency: b.currency(), ReturnLink: groupReturnLink,
		Payload: map[string]any{"plan_id": plan.ID, "price_id": price.ID, "days": price.DurationDays, "group_kind": kind, "group_id": groupID,
			"seats": seats, "seat_micros_per_day": price.PriceCents * 1_000_000 / int64(price.DurationDays)},
	}, nil
}

func groupRecordExists(tx *gorm.DB, source, ref string) bool {
	var n int64
	tx.Model(&GroupRecord{}).Where("source = ? AND source_ref = ?", source, ref).Count(&n)
	return n > 0
}

// fulfillGroupOrder 开通或续期团队会员（按订单号幂等）。有效期内同方案同席位顺延；否则从现在开始按新的方案与席位计算。
func fulfillGroupOrder(core plugincore.Core, userID uint, orderNo string, payload map[string]any) error {
	planID, days, seats := uint(payloadInt(payload["plan_id"])), int(payloadInt(payload["days"])), int(payloadInt(payload["seats"]))
	kind, _ := payload["group_kind"].(string)
	groupID := uint(payloadInt(payload["group_id"]))
	if planID == 0 || days <= 0 || seats <= 0 || kind == "" || groupID == 0 {
		return fmt.Errorf("订单 %s 的团队会员快照无效", orderNo)
	}
	var (
		sub    GroupMembership
		plan   Plan
		action string
		done   bool
	)
	now := time.Now()
	err := core.Gorm().Transaction(func(tx *gorm.DB) error {
		if groupRecordExists(tx, orderSource, orderNo) {
			done = true
			return nil
		}
		if err := tx.First(&plan, planID).Error; err != nil {
			return errPlanNotFound
		}
		exists := tx.Where("group_kind = ? AND group_id = ?", kind, groupID).First(&sub).Error == nil
		prev := sub.ExpiresAt
		switch {
		case exists && sub.ExpiresAt.After(now) && sub.PlanID == planID && sub.Seats == seats:
			action = ActionExtend
			sub.ExpiresAt = addDays(sub.ExpiresAt, days)
		case exists && sub.ExpiresAt.After(now):
			action = ActionSwitch
			sub.StartedAt, sub.ExpiresAt = now, addDays(now, days)
		default:
			action = ActionGrant
			sub.StartedAt, sub.ExpiresAt = now, addDays(now, days)
		}
		sub.GroupKind, sub.GroupID, sub.PlanID, sub.Seats, sub.BuyerID = kind, groupID, planID, seats, userID
		sub.SeatMicrosPerDay = payloadInt(payload["seat_micros_per_day"])
		sub.RemindedAt, sub.ExpiredNoticeAt = nil, nil
		if err := tx.Save(&sub).Error; err != nil {
			return err
		}
		rec := GroupRecord{GroupKind: kind, GroupID: groupID, PlanID: planID, PlanName: plan.Name, Action: action, Days: days, Seats: seats,
			ExpiresAt: &sub.ExpiresAt, Source: orderSource, SourceRef: orderNo, OperatorID: userID, Reason: orderNo}
		if exists {
			rec.PrevExpiresAt = &prev
		}
		return tx.Create(&rec).Error
	})
	if err != nil || done {
		return err
	}
	notifyGroupChange(core, sub, plan, userID, action != ActionExtend)
	return nil
}

// notifyGroupChange 通知购买人团队会员已开通 / 续期；新开通或更换方案时同时通知席位内的其他成员。
func notifyGroupChange(core plugincore.Core, sub GroupMembership, plan Plan, buyer uint, notifyMembers bool) {
	p, g, found := loadGroup(core, nil, sub.GroupKind, sub.GroupID)
	if !found {
		return
	}
	params := map[string]string{"group": g.Name, "plan": plan.Name, "seats": strconv.Itoa(sub.Seats), "date": formatDate(sub.ExpiresAt)}
	core.NotifyI18n(buyer, notificationType, "notify.membership.groupActive", params, map[string]any{"link": groupReturnLink})
	if !notifyMembers {
		return
	}
	for i, id := range p.Members(core, sub.GroupID) {
		if i >= sub.Seats {
			break
		}
		if id != buyer {
			core.NotifyI18n(id, notificationType, "notify.membership.groupCovered", params, map[string]any{"link": notificationLink})
		}
	}
}

// resolveSeatsProduct 增加席位：SKU = 组类型-组ID-增加数；价格按剩余天数（不足一天按一天）与开通时的每席每天单价折算。
func resolveSeatsProduct(core plugincore.Core, u *models.User, sku string) (plugincore.Product, error) {
	if !core.PluginEnabled(plugins.KeyMembership) {
		return plugincore.Product{}, errors.New("会员功能未启用")
	}
	parts, valid := parseGroupSKU(sku, 3)
	if !valid {
		return plugincore.Product{}, errors.New("商品不存在")
	}
	kind, groupID, add := parts[0], atoiUint(parts[1]), int(atoiUint(parts[2]))
	_, g, found := loadGroup(core, u, kind, groupID)
	if !found {
		return plugincore.Product{}, errors.New("团队不存在")
	}
	if !g.CanPurchase {
		return plugincore.Product{}, errGroupForbidden
	}
	now := time.Now()
	sub, active := activeGroupSub(core.Gorm(), kind, groupID, now)
	if !active {
		return plugincore.Product{}, errors.New("团队会员未开通或已到期，请先开通")
	}
	if add < 1 || sub.Seats+add > maxGroupSeats {
		return plugincore.Product{}, fmt.Errorf("增加后的席位数不能超过 %d", maxGroupSeats)
	}
	var plan Plan
	core.Gorm().First(&plan, sub.PlanID)
	days := int(math.Ceil(sub.ExpiresAt.Sub(now).Hours() / 24))
	amount := int64(math.Ceil(float64(sub.SeatMicrosPerDay) * float64(days) * float64(add) / 1_000_000))
	if amount < 1 {
		amount = 1
	}
	b := &behavior{core: core}
	return plugincore.Product{
		Kind: seatsProductKind, SKU: sku, Title: fmt.Sprintf("%s · %s：增加 %d 席（剩余 %d 天）", plan.Name, g.Name, add, days),
		AmountCents: amount, Currency: b.currency(), ReturnLink: groupReturnLink,
		Payload: map[string]any{"group_kind": kind, "group_id": groupID, "add": add, "plan_id": sub.PlanID},
	}, nil
}

// fulfillSeatsOrder 增加席位（按订单号幂等）；团队会员已到期或已更换方案时不再增加（只记流水）。
func fulfillSeatsOrder(core plugincore.Core, userID uint, orderNo string, payload map[string]any) error {
	kind, _ := payload["group_kind"].(string)
	groupID, add, planID := uint(payloadInt(payload["group_id"])), int(payloadInt(payload["add"])), uint(payloadInt(payload["plan_id"]))
	if kind == "" || groupID == 0 || add <= 0 {
		return fmt.Errorf("订单 %s 的席位快照无效", orderNo)
	}
	var (
		sub     GroupMembership
		plan    Plan
		applied bool
	)
	now := time.Now()
	err := core.Gorm().Transaction(func(tx *gorm.DB) error {
		if groupRecordExists(tx, orderSource, orderNo) {
			return nil
		}
		tx.First(&plan, planID)
		reason := orderNo
		if tx.Where("group_kind = ? AND group_id = ?", kind, groupID).First(&sub).Error == nil && sub.ExpiresAt.After(now) && sub.PlanID == planID {
			sub.Seats += add
			if sub.Seats > maxGroupSeats {
				sub.Seats = maxGroupSeats
			}
			if err := tx.Model(&GroupMembership{}).Where("id = ?", sub.ID).Update("seats", sub.Seats).Error; err != nil {
				return err
			}
			applied = true
		} else {
			reason = truncate(orderNo+"：团队会员已到期或已更换方案，未增加席位", 255)
		}
		return tx.Create(&GroupRecord{GroupKind: kind, GroupID: groupID, PlanID: planID, PlanName: plan.Name, Action: ActionSeats, Seats: sub.Seats,
			ExpiresAt: &sub.ExpiresAt, Source: orderSource, SourceRef: orderNo, OperatorID: userID, Reason: reason}).Error
	})
	if err == nil && applied {
		notifyGroupChange(core, sub, plan, userID, false)
	}
	return err
}

// refundGroupOrder 团队会员订单退款：选择撤销时按退款比例扣回天数，扣完则团队会员结束（按退款单号幂等）。
func refundGroupOrder(core plugincore.Core, ev plugincore.RefundEvent) error {
	if !ev.Revoke || ev.TotalCents <= 0 {
		return nil
	}
	return core.Gorm().Transaction(func(tx *gorm.DB) error {
		var granted GroupRecord
		if groupRecordExists(tx, refundSource, ev.RefundNo) || tx.Where("source = ? AND source_ref = ?", orderSource, ev.OrderNo).First(&granted).Error != nil {
			return nil
		}
		var sub GroupMembership
		if tx.Where("group_kind = ? AND group_id = ?", granted.GroupKind, granted.GroupID).First(&sub).Error != nil {
			return nil
		}
		days := proportionalDays(granted.Days, ev)
		prev, now := sub.ExpiresAt, time.Now()
		sub.ExpiresAt = addDays(sub.ExpiresAt, -days)
		action := ActionAdjust
		if !sub.ExpiresAt.After(now) {
			action, sub.ExpiresAt = ActionRevoke, now
		}
		if err := tx.Model(&GroupMembership{}).Where("id = ?", sub.ID).Updates(map[string]any{"expires_at": sub.ExpiresAt, "reminded_at": nil, "expired_notice_at": nil}).Error; err != nil {
			return err
		}
		return tx.Create(&GroupRecord{GroupKind: sub.GroupKind, GroupID: sub.GroupID, PlanID: sub.PlanID, PlanName: granted.PlanName, Action: action,
			Days: days, Seats: sub.Seats, PrevExpiresAt: &prev, ExpiresAt: &sub.ExpiresAt, Source: refundSource, SourceRef: ev.RefundNo,
			Reason: truncate(fmt.Sprintf("订单 %s 退款，扣回 %d 天", ev.OrderNo, days), 255)}).Error
	})
}

// refundSeatsOrder 增加席位的订单退款：选择撤销时按退款比例扣回席位（至少保留 1 席）。
func refundSeatsOrder(core plugincore.Core, ev plugincore.RefundEvent) error {
	if !ev.Revoke || ev.TotalCents <= 0 {
		return nil
	}
	add := int(payloadInt(ev.Payload["add"]))
	return core.Gorm().Transaction(func(tx *gorm.DB) error {
		var granted GroupRecord
		if groupRecordExists(tx, refundSource, ev.RefundNo) || tx.Where("source = ? AND source_ref = ?", orderSource, ev.OrderNo).First(&granted).Error != nil {
			return nil
		}
		var sub GroupMembership
		if tx.Where("group_kind = ? AND group_id = ?", granted.GroupKind, granted.GroupID).First(&sub).Error != nil {
			return nil
		}
		n := int(math.Round(float64(add) * float64(ev.AmountCents) / float64(ev.TotalCents)))
		sub.Seats -= n
		if sub.Seats < 1 {
			sub.Seats = 1
		}
		if err := tx.Model(&GroupMembership{}).Where("id = ?", sub.ID).Update("seats", sub.Seats).Error; err != nil {
			return err
		}
		return tx.Create(&GroupRecord{GroupKind: sub.GroupKind, GroupID: sub.GroupID, PlanID: sub.PlanID, PlanName: granted.PlanName, Action: ActionSeats,
			Seats: sub.Seats, ExpiresAt: &sub.ExpiresAt, Source: refundSource, SourceRef: ev.RefundNo,
			Reason: truncate(fmt.Sprintf("订单 %s 退款，扣回 %d 席", ev.OrderNo, n), 255)}).Error
	})
}

// —— 接口 ——

type groupSubView struct {
	Plan      *Plan     `json:"plan"`
	Seats     int       `json:"seats"`
	StartedAt time.Time `json:"started_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Active    bool      `json:"active"`
	DaysLeft  int       `json:"days_left"`
	Covered   int       `json:"covered"` // 当前被覆盖的成员数
}

func (b *behavior) subView(c *gin.Context, sub *GroupMembership, members []uint) *groupSubView {
	if sub == nil {
		return nil
	}
	now := time.Now()
	v := &groupSubView{Seats: sub.Seats, StartedAt: sub.StartedAt, ExpiresAt: sub.ExpiresAt, Active: sub.ExpiresAt.After(now)}
	if v.Active {
		v.DaysLeft = int(sub.ExpiresAt.Sub(now).Hours()/24) + 1
		v.Covered = len(members)
		if v.Covered > sub.Seats {
			v.Covered = sub.Seats
		}
	}
	plans := b.loadPlans(b.core.Gorm().Where("id = ?", sub.PlanID))
	if c != nil {
		b.localizePlans(c, plans)
	}
	if len(plans) == 1 {
		v.Plan = &plans[0]
	}
	return v
}

func (b *behavior) groupSub(kind string, id uint) *GroupMembership {
	var sub GroupMembership
	if b.core.Gorm().Where("group_kind = ? AND group_id = ?", kind, id).First(&sub).Error != nil {
		return nil
	}
	return &sub
}

// MyGroups GET /membership/groups 我能为之购买的组（如我管理的团队）及其团队会员，与可按团队购买的方案。
func (b *behavior) MyGroups(c *gin.Context) {
	u := b.core.CurrentUser(c)
	items := []gin.H{}
	for _, p := range plugincore.MemberGroupProviders(b.core) {
		for _, g := range p.Managed(b.core, u) {
			items = append(items, gin.H{"group": g, "subscription": b.subView(c, b.groupSub(g.Kind, g.ID), p.Members(b.core, g.ID))})
		}
	}
	plans := b.loadPlans(b.core.Gorm().Where("status = ? AND group_enabled = ?", "active", true))
	b.localizePlans(c, plans)
	b.core.OK(c, gin.H{"items": items, "plans": plans, "currency": b.currency(), "max_seats": maxGroupSeats})
}

type groupMemberView struct {
	ID          uint   `json:"id"`
	Username    string `json:"username"`
	Nickname    string `json:"nickname"`
	NameDisplay string `json:"name_display"`
	Avatar      string `json:"avatar"`
	Covered     bool   `json:"covered"`
}

// GroupDetail GET /membership/groups/:kind/:id 组的团队会员详情：席位覆盖的成员与流水（能管理该组的人可见）。
func (b *behavior) GroupDetail(c *gin.Context) {
	u := b.core.CurrentUser(c)
	id := atoiUint(c.Param("id"))
	p, g, found := loadGroup(b.core, u, c.Param("kind"), id)
	if !found || (!g.CanPurchase && !b.core.IsAdmin(u)) {
		b.core.Fail(c, http.StatusNotFound, "团队不存在")
		return
	}
	sub := b.groupSub(g.Kind, g.ID)
	memberIDs := p.Members(b.core, g.ID)
	var users []models.User
	b.core.Gorm().Select("id", "username", "nickname", "name_display", "avatar").Where("id IN ?", append(memberIDs, 0)).Find(&users)
	byID := map[uint]models.User{}
	for _, x := range users {
		byID[x.ID] = x
	}
	active := sub != nil && sub.ExpiresAt.After(time.Now())
	members := make([]groupMemberView, 0, len(memberIDs))
	for i, mid := range memberIDs {
		x := byID[mid]
		members = append(members, groupMemberView{ID: mid, Username: x.Username, Nickname: x.Nickname, NameDisplay: x.NameDisplay, Avatar: x.Avatar, Covered: active && i < sub.Seats})
	}
	var records []GroupRecord
	b.core.Gorm().Where("group_kind = ? AND group_id = ?", g.Kind, g.ID).Order("id DESC").Limit(20).Find(&records)
	b.core.OK(c, gin.H{"group": g, "subscription": b.subView(c, sub, memberIDs), "members": members, "records": records})
}

// myGroupCoverage 用户所在组中有效的团队会员（供「我的会员」展示是否在席位内）。
func (b *behavior) myGroupCoverage(c *gin.Context, u *models.User) []gin.H {
	out := []gin.H{}
	for _, cov := range userGroupCoverage(b.core, u.ID, time.Now()) {
		_, g, found := loadGroup(b.core, u, cov.Sub.GroupKind, cov.Sub.GroupID)
		if !found {
			continue
		}
		sub := cov.Sub
		v := b.subView(c, &sub, nil)
		out = append(out, gin.H{"group": g, "plan": v.Plan, "expires_at": sub.ExpiresAt, "days_left": v.DaysLeft, "covered": cov.Covered})
	}
	return out
}

// —— 管理员 ——

// AdminListGroups GET /admin/membership/groups 全部团队会员（含已到期）。
func (b *behavior) AdminListGroups(c *gin.Context) {
	page, pageSize := b.core.Paginate(c)
	db := b.core.Gorm().Model(&GroupMembership{})
	var total int64
	db.Count(&total)
	var subs []GroupMembership
	db.Order("expires_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&subs)
	u := b.core.CurrentUser(c)
	items := make([]gin.H, 0, len(subs))
	for i := range subs {
		s := subs[i]
		p, g, found := loadGroup(b.core, u, s.GroupKind, s.GroupID)
		members := []uint{}
		if found {
			members = p.Members(b.core, s.GroupID)
		} else {
			g = plugincore.MemberGroup{Kind: s.GroupKind, ID: s.GroupID}
		}
		items = append(items, gin.H{"group": g, "group_found": found, "subscription": b.subView(c, &s, members), "buyer_id": s.BuyerID})
	}
	b.core.OK(c, plugincore.PageResult{Items: items, Total: total, Page: page, PageSize: pageSize})
}

// AdminAdjustGroup PUT /admin/membership/groups/:kind/:id {plan_id, seats, expires_at} 开通或调整团队会员。
func (b *behavior) AdminAdjustGroup(c *gin.Context) {
	var req struct {
		PlanID    uint      `json:"plan_id"`
		Seats     int       `json:"seats"`
		ExpiresAt time.Time `json:"expires_at"`
		Reason    string    `json:"reason"`
	}
	if c.ShouldBindJSON(&req) != nil || req.Seats < 1 || req.Seats > maxGroupSeats || !req.ExpiresAt.After(time.Now()) {
		b.core.Fail(c, http.StatusBadRequest, fmt.Sprintf("请填写方案、1 到 %d 个席位与晚于现在的到期时间", maxGroupSeats))
		return
	}
	u := b.core.CurrentUser(c)
	kind, id := c.Param("kind"), atoiUint(c.Param("id"))
	_, g, found := loadGroup(b.core, u, kind, id)
	if !found {
		b.core.Fail(c, http.StatusNotFound, "团队不存在")
		return
	}
	var plan Plan
	if b.core.Gorm().First(&plan, req.PlanID).Error != nil {
		b.core.Fail(c, http.StatusBadRequest, errPlanNotFound.Error())
		return
	}
	now := time.Now()
	err := b.core.Gorm().Transaction(func(tx *gorm.DB) error {
		var sub GroupMembership
		exists := tx.Where("group_kind = ? AND group_id = ?", kind, id).First(&sub).Error == nil
		prev := sub.ExpiresAt
		if !exists || !sub.ExpiresAt.After(now) {
			sub.StartedAt = now
		}
		sub.GroupKind, sub.GroupID, sub.PlanID, sub.Seats, sub.ExpiresAt = kind, id, plan.ID, req.Seats, req.ExpiresAt
		sub.RemindedAt, sub.ExpiredNoticeAt = nil, nil
		if sub.BuyerID == 0 {
			sub.BuyerID = u.ID
		}
		if err := tx.Save(&sub).Error; err != nil {
			return err
		}
		rec := GroupRecord{GroupKind: kind, GroupID: id, PlanID: plan.ID, PlanName: plan.Name, Action: ActionAdjust, Seats: req.Seats,
			ExpiresAt: &sub.ExpiresAt, Source: "admin", OperatorID: u.ID, Reason: truncate(req.Reason, 255)}
		if exists {
			rec.PrevExpiresAt = &prev
		}
		return tx.Create(&rec).Error
	})
	if err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	b.core.RecordAudit(c, "membership.group_adjusted", "membership_group", kind+":"+strconv.FormatUint(uint64(id), 10), g.Name,
		map[string]any{"plan": plan.Name, "seats": req.Seats, "expires_at": formatDate(req.ExpiresAt)})
	b.core.OK(c, gin.H{"ok": true})
}

// AdminRevokeGroup POST /admin/membership/groups/:kind/:id/revoke 立即结束团队会员。
func (b *behavior) AdminRevokeGroup(c *gin.Context) {
	u := b.core.CurrentUser(c)
	kind, id := c.Param("kind"), atoiUint(c.Param("id"))
	sub := b.groupSub(kind, id)
	if sub == nil {
		b.core.Fail(c, http.StatusNotFound, "该团队没有团队会员")
		return
	}
	now := time.Now()
	prev := sub.ExpiresAt
	var plan Plan
	b.core.Gorm().First(&plan, sub.PlanID)
	err := b.core.Gorm().Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&GroupMembership{}).Where("id = ?", sub.ID).Updates(map[string]any{"expires_at": now, "expired_notice_at": now}).Error; err != nil {
			return err
		}
		return tx.Create(&GroupRecord{GroupKind: kind, GroupID: id, PlanID: sub.PlanID, PlanName: plan.Name, Action: ActionRevoke, Seats: sub.Seats,
			PrevExpiresAt: &prev, ExpiresAt: &now, Source: "admin", OperatorID: u.ID}).Error
	})
	if err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "取消失败")
		return
	}
	_, g, _ := loadGroup(b.core, u, kind, id)
	b.core.RecordAudit(c, "membership.group_revoked", "membership_group", kind+":"+strconv.FormatUint(uint64(id), 10), g.Name, nil)
	b.core.OK(c, gin.H{"ok": true})
}

// —— 到期提醒 ——

// sweepGroups 团队会员到期前提醒购买人、到期后通知（先占位再发，并发巡检只发一次）。
func (b *behavior) sweepGroups(now time.Time) {
	db := b.core.Gorm()
	notify := func(q *gorm.DB, key, column string) {
		var rows []GroupMembership
		if q.Limit(500).Find(&rows).Error != nil {
			return
		}
		for _, s := range rows {
			res := db.Model(&GroupMembership{}).Where("id = ? AND "+column+" IS NULL", s.ID).Update(column, now)
			if res.Error != nil || res.RowsAffected != 1 {
				continue
			}
			_, g, found := loadGroup(b.core, nil, s.GroupKind, s.GroupID)
			var plan Plan
			if !found || db.First(&plan, s.PlanID).Error != nil || s.BuyerID == 0 {
				continue
			}
			b.core.NotifyI18n(s.BuyerID, notificationType, key, map[string]string{"group": g.Name, "plan": plan.Name, "date": formatDate(s.ExpiresAt)}, map[string]any{"link": groupReturnLink})
		}
	}
	if days := b.reminderDays(); days > 0 {
		notify(db.Where("reminded_at IS NULL AND expires_at > ? AND expires_at <= ?", now, addDays(now, days)), "notify.membership.groupExpiring", "reminded_at")
	}
	notify(db.Where("expired_notice_at IS NULL AND expires_at <= ? AND expires_at > ?", now, addDays(now, -expiredNoticeRange)), "notify.membership.groupExpired", "expired_notice_at")
}
