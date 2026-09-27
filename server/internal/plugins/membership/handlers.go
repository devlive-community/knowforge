package membership

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

const maxPricesPerPlan = 20

func (b *behavior) Key() string { return plugins.KeyMembership }

func (b *behavior) RegisterRoutes(api *gin.RouterGroup, core plugincore.Core) {
	b.core = core
	feat := core.RequireFeaturePlugin(plugins.KeyMembership)
	// 公开：可开通的方案与价格
	api.GET("/membership/plans", core.OptionalAuth(), feat, b.PublicPlans)
	// 本人
	api.GET("/users/me/membership", core.RequireAuth(), feat, core.RequirePermissionMiddleware(PermRead), b.MyMembership)
	api.GET("/users/me/membership/gifts", core.RequireAuth(), feat, core.RequirePermissionMiddleware(PermRead), b.MyGifts)
	api.POST("/membership/redeem", core.RequireAuth(), feat, core.RequirePermissionMiddleware(PermRead), b.Redeem)
	api.POST("/membership/plans/:id/trial", core.RequireAuth(), feat, core.RequirePermissionMiddleware(PermRead), b.StartTrial)
	// 管理员
	adminGuard := []gin.HandlerFunc{core.RequireAuth(), core.RequireAdmin(), feat, core.RequirePermissionMiddleware(PermManage)}
	reg := func(method, path string, h gin.HandlerFunc) {
		api.Handle(method, path, append(append([]gin.HandlerFunc{}, adminGuard...), h)...)
	}
	reg(http.MethodGet, "/admin/membership/plans", b.AdminListPlans)
	reg(http.MethodPost, "/admin/membership/plans", b.AdminCreatePlan)
	reg(http.MethodPut, "/admin/membership/plans/:id", b.AdminUpdatePlan)
	reg(http.MethodDelete, "/admin/membership/plans/:id", b.AdminDeletePlan)
	reg(http.MethodGet, "/admin/membership/members", b.AdminListMembers)
	reg(http.MethodPost, "/admin/membership/grant", b.AdminGrant)
	reg(http.MethodPut, "/admin/membership/members/:user_id", b.AdminAdjust)
	reg(http.MethodPost, "/admin/membership/members/:user_id/revoke", b.AdminRevoke)
	reg(http.MethodGet, "/admin/membership/records", b.AdminListRecords)
	reg(http.MethodGet, "/admin/membership/redeem/batches", b.AdminListBatches)
	reg(http.MethodPost, "/admin/membership/redeem/batches", b.AdminCreateBatch)
	reg(http.MethodPut, "/admin/membership/redeem/batches/:id", b.AdminUpdateBatch)
	reg(http.MethodGet, "/admin/membership/redeem/batches/:id/codes", b.AdminBatchCodes)
	reg(http.MethodPut, "/admin/membership/redeem/codes/:id", b.AdminUpdateCode)
	reg(http.MethodGet, "/admin/membership/coupons", b.AdminListCoupons)
	reg(http.MethodPost, "/admin/membership/coupons", b.AdminCreateCoupon)
	reg(http.MethodPut, "/admin/membership/coupons/:id", b.AdminUpdateCoupon)
	reg(http.MethodGet, "/admin/membership/coupons/:id/uses", b.AdminCouponUses)
	reg(http.MethodGet, "/admin/membership/settings", b.AdminGetSettings)
	reg(http.MethodPut, "/admin/membership/settings", b.AdminUpdateSettings)
}

func changedFields(fields ...string) map[string]any { return map[string]any{"changed_fields": fields} }
func auditID(id uint) string                        { return strconv.FormatUint(uint64(id), 10) }

// —— 方案 ——

func (b *behavior) loadPlans(q *gorm.DB) []Plan {
	var plans []Plan
	q.Preload("Prices", func(db *gorm.DB) *gorm.DB { return db.Order("sort_order ASC, duration_days ASC, id ASC") }).
		Order("sort_order ASC, id ASC").Find(&plans)
	for i := range plans {
		if plans[i].Prices == nil {
			plans[i].Prices = []Price{}
		}
	}
	return plans
}

// localizePlans 按请求语言回退链替换方案名称/说明（未翻译的语言沿用默认语言）。
func (b *behavior) localizePlans(c *gin.Context, plans []Plan) {
	if len(plans) == 0 {
		return
	}
	ids := make([]uint, 0, len(plans))
	for _, p := range plans {
		ids = append(ids, p.ID)
	}
	resolved, _, err := b.core.LocalizeResources(c, resourceKind, ids)
	if err != nil {
		return
	}
	for i := range plans {
		for _, layer := range resolved[plans[i].ID].Layers {
			if v := layer.Fields["name"]; v != "" {
				plans[i].Name = v
			}
			if v, ok := layer.Fields["description"]; ok && layer.Fields["name"] != "" {
				plans[i].Description = v
			}
		}
	}
}

// PublicPlans GET /membership/plans 可开通的方案（仅启用中，含价格）与货币。
func (b *behavior) PublicPlans(c *gin.Context) {
	plans := b.loadPlans(b.core.Gorm().Where("status = ?", "active"))
	b.localizePlans(c, plans)
	b.core.OK(c, gin.H{"items": plans, "currency": b.currency()})
}

// MyMembership GET /users/me/membership 我的会员（含已到期的最近一次）与流水。
func (b *behavior) MyMembership(c *gin.Context) {
	u := b.core.CurrentUser(c)
	db := b.core.Gorm()
	var out gin.H
	var m UserMembership
	if db.Where("user_id = ?", u.ID).First(&m).Error == nil {
		plans := b.loadPlans(db.Where("id = ?", m.PlanID))
		b.localizePlans(c, plans)
		var plan any
		if len(plans) == 1 {
			plan = plans[0]
		}
		now := time.Now()
		active := m.ExpiresAt.After(now)
		daysLeft := 0
		if active {
			daysLeft = int(m.ExpiresAt.Sub(now).Hours()/24) + 1
		}
		out = gin.H{"plan": plan, "started_at": m.StartedAt, "expires_at": m.ExpiresAt, "active": active, "days_left": daysLeft, "trial": m.Trial}
	}
	var records []Record
	db.Where("user_id = ?", u.ID).Order("id DESC").Limit(20).Find(&records)
	blocker := b.trialBlocker(u)
	b.core.OK(c, gin.H{"membership": out, "records": records, "currency": b.currency(),
		"trial": gin.H{"eligible": blocker == nil, "needs_verified_email": errors.Is(blocker, errTrialUnverified)}})
}

// AdminListPlans GET /admin/membership/plans 全部方案（含归档）、价格、多语言内容与会员数。
func (b *behavior) AdminListPlans(c *gin.Context) {
	db := b.core.Gorm()
	plans := b.loadPlans(db)
	type countRow struct {
		PlanID uint
		N      int64
	}
	var rows []countRow
	db.Model(&UserMembership{}).Select("plan_id, COUNT(*) AS n").Where("expires_at > ?", time.Now()).Group("plan_id").Scan(&rows)
	active := map[uint]int64{}
	for _, r := range rows {
		active[r.PlanID] = r.N
	}
	items := make([]gin.H, 0, len(plans))
	for i := range plans {
		if tr, err := b.core.LoadResourceTranslations(db, resourceKind, plans[i].ID); err == nil {
			plans[i].Translations, _ = json.Marshal(tr)
		}
		items = append(items, gin.H{"plan": plans[i], "active_members": active[plans[i].ID]})
	}
	b.core.OK(c, gin.H{"items": items, "currency": b.currency()})
}

type priceInput struct {
	ID                 uint  `json:"id"`
	DurationDays       int   `json:"duration_days"`
	PriceCents         int64 `json:"price_cents"`
	OriginalPriceCents int64 `json:"original_price_cents"`
}

type planRequest struct {
	Name         string                                    `json:"name"`
	Description  string                                    `json:"description"`
	IconType     string                                    `json:"icon_type"`
	IconValue    string                                    `json:"icon_value"`
	Color        string                                    `json:"color"`
	Entitlements models.EntitlementMap                     `json:"entitlements"`
	Status       string                                    `json:"status"`
	SortOrder    int                                       `json:"sort_order"`
	TrialDays    int                                       `json:"trial_days"`
	Prices       []priceInput                              `json:"prices"`
	Translations map[string]plugincore.ResourceTranslation `json:"translations"`
}

// validate 规范化并校验方案请求（多语言名称在 prepareTexts 中处理）。
func (req *planRequest) validate() error {
	if req.IconType != "image" && req.IconType != "svg" {
		req.IconType = "fa"
	}
	if strings.TrimSpace(req.IconValue) == "" {
		req.IconValue = "fa-crown"
	}
	if req.Status != "archived" {
		req.Status = "active"
	}
	if len(req.Color) > 20 {
		return errors.New("颜色格式无效")
	}
	if err := plugincore.ValidateEntitlementMap(req.Entitlements); err != nil {
		return err
	}
	if req.TrialDays < 0 || req.TrialDays > maxTrialDays {
		return fmt.Errorf("试用天数需在 0 到 %d 之间（0 为不提供试用）", maxTrialDays)
	}
	if len(req.Prices) > maxPricesPerPlan {
		return fmt.Errorf("每个方案最多 %d 档价格", maxPricesPerPlan)
	}
	seen := map[int]bool{}
	for _, p := range req.Prices {
		if p.DurationDays < 1 || p.DurationDays > maxDurationDays {
			return errBadDuration
		}
		if seen[p.DurationDays] {
			return fmt.Errorf("时长 %d 天的价格重复", p.DurationDays)
		}
		seen[p.DurationDays] = true
		if p.PriceCents < 1 || p.PriceCents > maxPriceCents {
			return errors.New("价格需大于 0")
		}
		if p.OriginalPriceCents != 0 && (p.OriginalPriceCents < p.PriceCents || p.OriginalPriceCents > maxPriceCents) {
			return errors.New("划线价需不低于售价")
		}
	}
	return nil
}

// prepareTexts 方案名称/说明取默认语言的已发布翻译（旧式请求只传 name/description 时视为默认语言并发布）。
func (b *behavior) prepareTexts(req *planRequest, id uint) error {
	def, err := b.core.DefaultContentLocale()
	if err != nil {
		return err
	}
	existing, err := b.core.LoadResourceTranslations(b.core.Gorm(), resourceKind, id)
	if err != nil {
		return err
	}
	if req.Translations == nil {
		req.Translations = map[string]plugincore.ResourceTranslation{}
		if strings.TrimSpace(req.Name) != "" {
			req.Translations[def] = plugincore.ResourceTranslation{
				Fields:   map[string]string{"name": strings.TrimSpace(req.Name), "description": req.Description},
				Revision: existing[def].Revision, Publish: true,
			}
		}
	}
	published := existing[def].Published
	if tr, ok := req.Translations[def]; ok && tr.Publish {
		published = tr.Fields
	}
	if strings.TrimSpace(published["name"]) == "" {
		return errors.New("请填写并发布默认语言的方案名称")
	}
	req.Name = strings.TrimSpace(published["name"])
	req.Description = published["description"]
	return nil
}

// savePrices 按请求整体替换方案价格：带 id 的更新，不带的新建，缺失的删除。
func savePrices(tx *gorm.DB, planID uint, inputs []priceInput) error {
	var existing []Price
	if err := tx.Where("plan_id = ?", planID).Find(&existing).Error; err != nil {
		return err
	}
	keep := map[uint]bool{}
	for i, in := range inputs {
		p := Price{PlanID: planID, DurationDays: in.DurationDays, PriceCents: in.PriceCents, OriginalPriceCents: in.OriginalPriceCents, SortOrder: i}
		owned := false
		for _, e := range existing {
			if in.ID != 0 && e.ID == in.ID {
				owned = true
			}
		}
		if owned {
			keep[in.ID] = true
			if err := tx.Model(&Price{}).Where("id = ?", in.ID).Updates(map[string]any{
				"duration_days": p.DurationDays, "price_cents": p.PriceCents, "original_price_cents": p.OriginalPriceCents, "sort_order": i,
			}).Error; err != nil {
				return err
			}
			continue
		}
		if err := tx.Create(&p).Error; err != nil {
			return err
		}
		keep[p.ID] = true
	}
	for _, e := range existing {
		if !keep[e.ID] {
			if err := tx.Delete(&Price{}, e.ID).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *behavior) failSave(c *gin.Context, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, plugincore.ErrTranslationConflict) {
		status = http.StatusConflict
	}
	b.core.Fail(c, status, err.Error())
}

// AdminCreatePlan POST /admin/membership/plans
func (b *behavior) AdminCreatePlan(c *gin.Context) {
	var req planRequest
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if err := req.validate(); err != nil {
		b.core.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := b.prepareTexts(&req, 0); err != nil {
		b.core.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	actor := b.core.CurrentUser(c).ID
	plan := Plan{Name: req.Name, Description: req.Description, IconType: req.IconType, IconValue: req.IconValue, Color: req.Color,
		Entitlements: req.Entitlements, Status: req.Status, SortOrder: req.SortOrder, TrialDays: req.TrialDays}
	if err := b.core.Gorm().Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&plan).Error; err != nil {
			return err
		}
		// status 列带 default，显式写回一次，避免「归档」被默认值覆盖
		if err := tx.Model(&Plan{}).Where("id = ?", plan.ID).Update("status", req.Status).Error; err != nil {
			return err
		}
		if err := savePrices(tx, plan.ID, req.Prices); err != nil {
			return err
		}
		return b.core.SaveResourceTranslations(tx, resourceKind, plan.ID, actor, req.Translations)
	}); err != nil {
		b.failSave(c, err)
		return
	}
	b.core.RecordAudit(c, "membership.plan_created", "membership_plan", auditID(plan.ID), plan.Name, changedFields("name", "entitlements", "prices", "status", "trial_days"))
	b.respondPlan(c, plan.ID)
}

// AdminUpdatePlan PUT /admin/membership/plans/:id
func (b *behavior) AdminUpdatePlan(c *gin.Context) {
	var plan Plan
	if b.core.Gorm().First(&plan, c.Param("id")).Error != nil {
		b.core.Fail(c, http.StatusNotFound, errPlanNotFound.Error())
		return
	}
	var req planRequest
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if err := req.validate(); err != nil {
		b.core.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := b.prepareTexts(&req, plan.ID); err != nil {
		b.core.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	actor := b.core.CurrentUser(c).ID
	if err := b.core.Gorm().Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Plan{}).Where("id = ?", plan.ID).Updates(map[string]any{
			"name": req.Name, "description": req.Description, "icon_type": req.IconType, "icon_value": req.IconValue,
			"color": req.Color, "entitlements": req.Entitlements, "status": req.Status, "sort_order": req.SortOrder, "trial_days": req.TrialDays,
		}).Error; err != nil {
			return err
		}
		if err := savePrices(tx, plan.ID, req.Prices); err != nil {
			return err
		}
		return b.core.SaveResourceTranslations(tx, resourceKind, plan.ID, actor, req.Translations)
	}); err != nil {
		b.failSave(c, err)
		return
	}
	b.core.RecordAudit(c, "membership.plan_updated", "membership_plan", auditID(plan.ID), req.Name, changedFields("name", "entitlements", "prices", "status", "trial_days"))
	b.respondPlan(c, plan.ID)
}

func (b *behavior) respondPlan(c *gin.Context, id uint) {
	plans := b.loadPlans(b.core.Gorm().Where("id = ?", id))
	if len(plans) != 1 {
		b.core.Fail(c, http.StatusNotFound, errPlanNotFound.Error())
		return
	}
	if tr, err := b.core.LoadResourceTranslations(b.core.Gorm(), resourceKind, id); err == nil {
		plans[0].Translations, _ = json.Marshal(tr)
	}
	b.core.OK(c, plans[0])
}

// AdminDeletePlan DELETE /admin/membership/plans/:id 仅可删除从未有会员使用过的方案（否则请归档）。
func (b *behavior) AdminDeletePlan(c *gin.Context) {
	db := b.core.Gorm()
	var plan Plan
	if db.First(&plan, c.Param("id")).Error != nil {
		b.core.Fail(c, http.StatusNotFound, errPlanNotFound.Error())
		return
	}
	var used int64
	db.Model(&UserMembership{}).Where("plan_id = ?", plan.ID).Count(&used)
	if used > 0 {
		b.core.Fail(c, http.StatusConflict, "已有用户持有该方案，不能删除，请改为归档")
		return
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("plan_id = ?", plan.ID).Delete(&Price{}).Error; err != nil {
			return err
		}
		if err := tx.Where("resource_type = ? AND resource_id = ?", resourceKind, plan.ID).Delete(&models.LocalizedResourceContent{}).Error; err != nil {
			return err
		}
		return tx.Delete(&plan).Error
	}); err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "删除失败: "+err.Error())
		return
	}
	b.core.RecordAudit(c, "membership.plan_deleted", "membership_plan", auditID(plan.ID), plan.Name, changedFields("deleted"))
	b.core.OK(c, gin.H{"message": "已删除"})
}

// —— 会员 ——

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

func (b *behavior) planBriefs() map[uint]gin.H {
	var plans []Plan
	b.core.Gorm().Find(&plans)
	out := map[uint]gin.H{}
	for _, p := range plans {
		out[p.ID] = gin.H{"id": p.ID, "name": p.Name, "icon_type": p.IconType, "icon_value": p.IconValue, "color": p.Color, "status": p.Status}
	}
	return out
}

// AdminListMembers GET /admin/membership/members?q=&status=active|expired&plan_id=&page=&page_size=
func (b *behavior) AdminListMembers(c *gin.Context) {
	page, pageSize := b.core.Paginate(c)
	now := time.Now()
	q := b.core.Gorm().Model(&UserMembership{})
	if kw := strings.TrimSpace(c.Query("q")); kw != "" {
		like := "%" + kw + "%"
		q = q.Where("user_id IN (?)", b.core.Gorm().Model(&models.User{}).Select("id").Where("username LIKE ? OR email LIKE ? OR nickname LIKE ?", like, like, like))
	}
	switch c.Query("status") {
	case "active":
		q = q.Where("expires_at > ?", now)
	case "expired":
		q = q.Where("expires_at <= ?", now)
	}
	if pid := b.core.AtoiDefault(c.Query("plan_id"), 0); pid > 0 {
		q = q.Where("plan_id = ?", pid)
	}
	var total int64
	q.Count(&total)
	var rows []UserMembership
	q.Order("expires_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows)
	ids := make([]uint, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.UserID)
	}
	users, plans := b.userBriefs(ids), b.planBriefs()
	items := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		items = append(items, gin.H{"user": users[r.UserID], "plan": plans[r.PlanID], "started_at": r.StartedAt, "expires_at": r.ExpiresAt, "active": r.ExpiresAt.After(now), "trial": r.Trial})
	}
	b.core.OK(c, plugincore.PageResult{Items: items, Total: total, Page: page, PageSize: pageSize})
}

func (b *behavior) findUser(id uint) (*models.User, bool) {
	var u models.User
	if id == 0 || b.core.Gorm().First(&u, id).Error != nil {
		return nil, false
	}
	return &u, true
}

// AdminGrant POST /admin/membership/grant {user_id, plan_id, days, reason} 开通/续期（同方案顺延，换方案从现在起算）。
func (b *behavior) AdminGrant(c *gin.Context) {
	var req struct {
		UserID uint   `json:"user_id"`
		PlanID uint   `json:"plan_id"`
		Days   int    `json:"days"`
		Reason string `json:"reason"`
	}
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	u, found := b.findUser(req.UserID)
	if !found {
		b.core.Fail(c, http.StatusNotFound, "用户不存在")
		return
	}
	var (
		m      UserMembership
		plan   Plan
		action string
	)
	err := b.core.Gorm().Transaction(func(tx *gorm.DB) error {
		var err error
		m, plan, action, err = applyGrant(tx, grant{UserID: u.ID, PlanID: req.PlanID, Days: req.Days, Source: "admin",
			OperatorID: b.core.CurrentUser(c).ID, Reason: truncate(req.Reason, 255)}, time.Now())
		return err
	})
	if err != nil {
		b.core.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	b.notifyChange(u.ID, plan, action, m.ExpiresAt)
	b.core.RecordAudit(c, "membership.granted", "membership", auditID(u.ID), u.Username,
		map[string]any{"changed_fields": []string{"plan", "expires_at"}, "plan": plan.Name, "action": action, "days": req.Days})
	b.core.OK(c, gin.H{"action": action, "plan_id": m.PlanID, "started_at": m.StartedAt, "expires_at": m.ExpiresAt})
}

// AdminAdjust PUT /admin/membership/members/:user_id {plan_id, expires_at, reason} 直接设置方案与到期时间。
func (b *behavior) AdminAdjust(c *gin.Context) {
	var req struct {
		PlanID    uint      `json:"plan_id"`
		ExpiresAt time.Time `json:"expires_at"`
		Reason    string    `json:"reason"`
	}
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	u, found := b.findUser(uint(b.core.AtoiDefault(c.Param("user_id"), 0)))
	if !found {
		b.core.Fail(c, http.StatusNotFound, "用户不存在")
		return
	}
	now := time.Now()
	if !req.ExpiresAt.After(now) || req.ExpiresAt.After(addDays(now, maxDurationDays)) {
		b.core.Fail(c, http.StatusBadRequest, "到期时间需晚于当前时间且不超过 10 年")
		return
	}
	var plan Plan
	if b.core.Gorm().First(&plan, req.PlanID).Error != nil {
		b.core.Fail(c, http.StatusBadRequest, errPlanNotFound.Error())
		return
	}
	var m UserMembership
	err := b.core.Gorm().Transaction(func(tx *gorm.DB) error {
		exists := tx.Where("user_id = ?", u.ID).First(&m).Error == nil
		var prev *time.Time
		if exists {
			p := m.ExpiresAt
			prev = &p
		}
		if !exists || m.PlanID != plan.ID || !m.ExpiresAt.After(now) {
			m.StartedAt = now
		}
		m.UserID, m.PlanID, m.ExpiresAt, m.Trial = u.ID, plan.ID, req.ExpiresAt, false
		if err := saveMembership(tx, &m, exists); err != nil {
			return err
		}
		expires := m.ExpiresAt
		return tx.Create(&Record{UserID: u.ID, PlanID: plan.ID, PlanName: plan.Name, Action: ActionAdjust, PrevExpiresAt: prev, ExpiresAt: &expires,
			Source: "admin", OperatorID: b.core.CurrentUser(c).ID, Reason: truncate(req.Reason, 255)}).Error
	})
	if err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}
	b.notifyChange(u.ID, plan, ActionAdjust, m.ExpiresAt)
	b.core.RecordAudit(c, "membership.adjusted", "membership", auditID(u.ID), u.Username,
		map[string]any{"changed_fields": []string{"plan", "expires_at"}, "plan": plan.Name})
	b.core.OK(c, gin.H{"plan_id": m.PlanID, "started_at": m.StartedAt, "expires_at": m.ExpiresAt})
}

// AdminRevoke POST /admin/membership/members/:user_id/revoke {reason} 取消会员（立即失效，流水保留）。
func (b *behavior) AdminRevoke(c *gin.Context) {
	var req struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&req)
	u, found := b.findUser(uint(b.core.AtoiDefault(c.Param("user_id"), 0)))
	if !found {
		b.core.Fail(c, http.StatusNotFound, "用户不存在")
		return
	}
	var m UserMembership
	var plan Plan
	err := b.core.Gorm().Transaction(func(tx *gorm.DB) error {
		if tx.Where("user_id = ?", u.ID).First(&m).Error != nil {
			return errors.New("该用户没有会员")
		}
		tx.First(&plan, m.PlanID)
		if err := tx.Where("user_id = ?", u.ID).Delete(&UserMembership{}).Error; err != nil {
			return err
		}
		prev := m.ExpiresAt
		return tx.Create(&Record{UserID: u.ID, PlanID: m.PlanID, PlanName: plan.Name, Action: ActionRevoke, PrevExpiresAt: &prev,
			Source: "admin", OperatorID: b.core.CurrentUser(c).ID, Reason: truncate(req.Reason, 255)}).Error
	})
	if err != nil {
		b.core.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if m.ExpiresAt.After(time.Now()) {
		b.core.NotifyI18n(u.ID, notificationType, "notify.membership.revoked", map[string]string{"plan": plan.Name}, map[string]any{"link": notificationLink})
	}
	b.core.RecordAudit(c, "membership.revoked", "membership", auditID(u.ID), u.Username, map[string]any{"changed_fields": []string{"plan", "expires_at"}, "plan": plan.Name})
	b.core.OK(c, gin.H{"message": "已取消"})
}

// AdminListRecords GET /admin/membership/records?user_id=&page=&page_size= 会员流水。
func (b *behavior) AdminListRecords(c *gin.Context) {
	page, pageSize := b.core.Paginate(c)
	q := b.core.Gorm().Model(&Record{})
	if uid := b.core.AtoiDefault(c.Query("user_id"), 0); uid > 0 {
		q = q.Where("user_id = ?", uid)
	}
	var total int64
	q.Count(&total)
	var rows []Record
	q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows)
	ids := make([]uint, 0, len(rows)*2)
	for _, r := range rows {
		ids = append(ids, r.UserID)
		if r.OperatorID != 0 {
			ids = append(ids, r.OperatorID)
		}
	}
	users := b.userBriefs(ids)
	items := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		item := gin.H{"record": r, "user": users[r.UserID]}
		if op, ok := users[r.OperatorID]; ok {
			item["operator"] = op
		}
		items = append(items, item)
	}
	b.core.OK(c, plugincore.PageResult{Items: items, Total: total, Page: page, PageSize: pageSize})
}

// —— 设置 ——

type settingsPayload struct {
	Currency           string `json:"currency"`
	ReminderDays       int    `json:"reminder_days"`
	TrialVerifiedEmail bool   `json:"trial_verified_email"`
}

// AdminGetSettings GET /admin/membership/settings
func (b *behavior) AdminGetSettings(c *gin.Context) {
	b.core.OK(c, settingsPayload{Currency: b.currency(), ReminderDays: b.reminderDays(), TrialVerifiedEmail: b.trialNeedsVerifiedEmail()})
}

// AdminUpdateSettings PUT /admin/membership/settings {currency?, reminder_days?}
func (b *behavior) AdminUpdateSettings(c *gin.Context) {
	var req struct {
		Currency      *string `json:"currency"`
		ReminderDays  *int    `json:"reminder_days"`
		TrialVerified *bool   `json:"trial_verified_email"`
	}
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	fields := []string{}
	if req.Currency != nil {
		v := strings.ToUpper(strings.TrimSpace(*req.Currency))
		if !validCurrency(v) {
			b.core.Fail(c, http.StatusBadRequest, "货币需为 3 位字母代码（如 CNY、USD）")
			return
		}
		if err := b.core.SetSetting(cfgCurrency, v, "会员：价格货币（ISO 4217）"); err != nil {
			b.core.Fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
		fields = append(fields, "currency")
	}
	if req.ReminderDays != nil {
		if *req.ReminderDays < 0 || *req.ReminderDays > maxReminderDays {
			b.core.Fail(c, http.StatusBadRequest, fmt.Sprintf("到期提醒需在 0 到 %d 天之间（0 为不提醒）", maxReminderDays))
			return
		}
		if err := b.core.SetSetting(cfgReminderDays, strconv.Itoa(*req.ReminderDays), "会员：到期前提醒天数（0 不提醒）"); err != nil {
			b.core.Fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
		fields = append(fields, "reminder_days")
	}
	if req.TrialVerified != nil {
		if err := b.core.SetSetting(cfgTrialVerified, strconv.FormatBool(*req.TrialVerified), "会员：试用需先验证邮箱"); err != nil {
			b.core.Fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
		fields = append(fields, "trial_verified_email")
	}
	sort.Strings(fields)
	b.core.RecordAudit(c, "membership.settings_updated", "membership", "settings", "会员设置", changedFields(fields...))
	b.AdminGetSettings(c)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
