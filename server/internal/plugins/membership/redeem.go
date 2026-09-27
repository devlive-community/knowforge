package membership

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 兑换码：管理员按批次生成（批量一次性卡密，或一个可多次使用的活动码），用户输入兑换码开通/续期会员（经 applyGrant，流水来源 redeem）。
// 同一用户对同一批次只能兑换一次；兑换码不区分大小写、忽略空格与连字符；连续输错会被暂时限制，防止穷举。

const (
	codeAlphabet     = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // 去掉易混淆的 0/O/1/I
	codeLength       = 16
	maxBatchCodes    = 10000
	maxCodeUses      = 1_000_000
	redeemFailLimit  = 10 // 每小时最多输错次数
	redeemFailWindow = time.Hour

	sourceRedeem = "redeem"
)

// RedeemBatch 一批兑换码。
type RedeemBatch struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	Name      string     `gorm:"size:120" json:"name"`
	PlanID    uint       `gorm:"index" json:"plan_id"`
	Days      int        `json:"days"`
	Kind      string     `gorm:"size:10" json:"kind"` // cards（一批一次性卡密）| promo（一个可多次使用的活动码）| gift（礼品卡）
	MaxUses   int        `json:"max_uses"`            // 每个码最多可兑换次数
	ExpiresAt *time.Time `json:"expires_at"`          // 兑换截止时间（空为不限）
	Status    string     `gorm:"size:10;index" json:"status"`
	CreatedBy uint       `json:"created_by"`
	OrderNo   string     `gorm:"size:64;index" json:"order_no,omitempty"` // 礼品卡的购买订单
	CreatedAt time.Time  `json:"created_at"`
}

func (RedeemBatch) TableName() string { return "membership_redeem_batches" }

// RedeemCode 一个兑换码（Code 为规范化后的形式：大写、去掉空格与连字符）。
type RedeemCode struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	BatchID   uint      `gorm:"index" json:"batch_id"`
	Code      string    `gorm:"size:40;uniqueIndex" json:"code"`
	MaxUses   int       `json:"max_uses"`
	UsedCount int       `json:"used_count"`
	Disabled  bool      `json:"disabled"`
	OwnerID   uint      `gorm:"index" json:"owner_id"` // 礼品卡的购买者（其他为 0）
	CreatedAt time.Time `json:"created_at"`
}

func (RedeemCode) TableName() string { return "membership_redeem_codes" }

// RedeemUse 一次兑换（同一用户对同一批次唯一）。
type RedeemUse struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	BatchID   uint      `gorm:"uniqueIndex:idx_redeem_use" json:"batch_id"`
	UserID    uint      `gorm:"uniqueIndex:idx_redeem_use" json:"user_id"`
	CodeID    uint      `gorm:"index" json:"code_id"`
	CreatedAt time.Time `json:"created_at"`
}

func (RedeemUse) TableName() string { return "membership_redeem_uses" }

// normalizeCode 兑换码规范化：大写、去掉空白与连字符。
func normalizeCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if r == '-' || r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// formatCode 卡密的展示形式：每 4 位一组。
func formatCode(code string) string {
	if len(code) != codeLength {
		return code
	}
	return code[0:4] + "-" + code[4:8] + "-" + code[8:12] + "-" + code[12:16]
}

func randomCode() (string, error) {
	var b strings.Builder
	max := big.NewInt(int64(len(codeAlphabet)))
	for i := 0; i < codeLength; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b.WriteByte(codeAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// createCodes 在事务内为批次生成 n 个随机码（冲突时重试）。
func createCodes(tx *gorm.DB, batch RedeemBatch, n int, ownerID uint) ([]RedeemCode, error) {
	codes := make([]RedeemCode, 0, n)
	seen := map[string]bool{}
	for len(codes) < n {
		code, err := randomCode()
		if err != nil {
			return nil, err
		}
		if seen[code] {
			continue
		}
		var exists int64
		tx.Model(&RedeemCode{}).Where("code = ?", code).Count(&exists)
		if exists > 0 {
			continue
		}
		seen[code] = true
		codes = append(codes, RedeemCode{BatchID: batch.ID, Code: code, MaxUses: batch.MaxUses, OwnerID: ownerID})
	}
	if err := tx.CreateInBatches(&codes, 500).Error; err != nil {
		return nil, err
	}
	return codes, nil
}

// —— 防穷举：按用户统计最近一小时的错误次数（存数据库，多实例共享）——

// RedeemFail 一次输错的兑换码（只用于限制穷举，超过统计窗口后清理）。
type RedeemFail struct {
	ID        uint      `gorm:"primaryKey"`
	UserID    uint      `gorm:"index:idx_redeem_fail"`
	CreatedAt time.Time `gorm:"index:idx_redeem_fail"`
}

func (RedeemFail) TableName() string { return "membership_redeem_fails" }

func (b *behavior) recentFails(userID uint, now time.Time) int {
	var n int64
	b.core.Gorm().Model(&RedeemFail{}).Where("user_id = ? AND created_at > ?", userID, now.Add(-redeemFailWindow)).Count(&n)
	return int(n)
}

func (b *behavior) recordFail(userID uint, now time.Time) {
	db := b.core.Gorm()
	db.Create(&RedeemFail{UserID: userID, CreatedAt: now})
	db.Where("created_at < ?", now.Add(-redeemFailWindow)).Delete(&RedeemFail{})
}

var (
	errCodeInvalid  = errors.New("兑换码无效")
	errCodeUsedUp   = errors.New("兑换码已被使用")
	errCodeExpired  = errors.New("兑换码已过期")
	errCodeDisabled = errors.New("兑换码已停用")
	errCodeRedeemed = errors.New("你已兑换过这一批兑换码")
)

// redeemed 一次兑换的结果。
type redeemed struct {
	m         UserMembership
	plan      Plan
	action    string
	giftOwner uint // 兑换的是他人送出的礼品卡时为购买者
}

// redeem 兑换（在事务内原子地占用一次使用次数并开通会员）。
func (b *behavior) redeem(userID uint, raw string, now time.Time) (redeemed, error) {
	code := normalizeCode(raw)
	var r redeemed
	if code == "" || utf8.RuneCountInString(code) > 40 {
		return r, errCodeInvalid
	}
	err := b.core.Gorm().Transaction(func(tx *gorm.DB) error {
		var rc RedeemCode
		if tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("code = ?", code).First(&rc).Error != nil {
			return errCodeInvalid
		}
		var batch RedeemBatch
		if tx.First(&batch, rc.BatchID).Error != nil {
			return errCodeInvalid
		}
		switch {
		case rc.Disabled || batch.Status != "active":
			return errCodeDisabled
		case batch.ExpiresAt != nil && now.After(*batch.ExpiresAt):
			return errCodeExpired
		case rc.UsedCount >= rc.MaxUses:
			return errCodeUsedUp
		}
		if res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&RedeemUse{BatchID: batch.ID, UserID: userID, CodeID: rc.ID}); res.Error != nil || res.RowsAffected == 0 {
			return errCodeRedeemed
		}
		// 条件更新防止并发超用
		if res := tx.Model(&RedeemCode{}).Where("id = ? AND used_count < max_uses", rc.ID).Update("used_count", gorm.Expr("used_count + 1")); res.RowsAffected == 0 {
			return errCodeUsedUp
		}
		if batch.Kind == kindGift && rc.OwnerID != userID {
			r.giftOwner = rc.OwnerID
		}
		var err error
		r.m, r.plan, r.action, err = applyGrant(tx, grant{UserID: userID, PlanID: batch.PlanID, Days: batch.Days, Source: sourceRedeem,
			SourceRef: formatCode(rc.Code), Reason: batch.Name, AllowArchived: batch.Kind == kindGift}, now)
		return err
	})
	return r, err
}

// Redeem POST /membership/redeem {code} 兑换会员。
func (b *behavior) Redeem(c *gin.Context) {
	var req struct {
		Code string `json:"code"`
	}
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	u := b.core.CurrentUser(c)
	now := time.Now()
	if b.recentFails(u.ID, now) >= redeemFailLimit {
		b.core.Fail(c, http.StatusTooManyRequests, "输错次数过多，请一小时后再试")
		return
	}
	r, err := b.redeem(u.ID, req.Code, now)
	if err != nil {
		if errors.Is(err, errCodeInvalid) {
			b.recordFail(u.ID, now)
		}
		b.core.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	b.notifyChange(u.ID, r.plan, r.action, r.m.ExpiresAt)
	if r.giftOwner != 0 {
		b.core.NotifyI18n(r.giftOwner, notificationType, "notify.membership.giftRedeemed", map[string]string{"plan": r.plan.Name}, map[string]any{"link": notificationLink})
	}
	b.core.OK(c, gin.H{"action": r.action, "plan": gin.H{"id": r.plan.ID, "name": r.plan.Name}, "expires_at": r.m.ExpiresAt})
}

// —— 管理员 ——

type batchView struct {
	RedeemBatch
	PlanName  string `json:"plan_name"`
	Codes     int64  `json:"codes"`
	Redeemed  int64  `json:"redeemed"` // 兑换人次
	PromoCode string `json:"promo_code,omitempty"`
}

func (b *behavior) batchViews(rows []RedeemBatch) []batchView {
	db := b.core.Gorm()
	plans := b.planBriefs()
	out := make([]batchView, 0, len(rows))
	for _, r := range rows {
		v := batchView{RedeemBatch: r}
		if p, ok := plans[r.PlanID]; ok {
			v.PlanName, _ = p["name"].(string)
		}
		db.Model(&RedeemCode{}).Where("batch_id = ?", r.ID).Count(&v.Codes)
		db.Model(&RedeemUse{}).Where("batch_id = ?", r.ID).Count(&v.Redeemed)
		if r.Kind == "promo" {
			var rc RedeemCode
			if db.Where("batch_id = ?", r.ID).First(&rc).Error == nil {
				v.PromoCode = rc.Code
			}
		}
		out = append(out, v)
	}
	return out
}

// AdminListBatches GET /admin/membership/redeem/batches?page=
func (b *behavior) AdminListBatches(c *gin.Context) {
	page, pageSize := b.core.Paginate(c)
	q := b.core.Gorm().Model(&RedeemBatch{}).Where("kind <> ?", kindGift)
	var total int64
	q.Count(&total)
	var rows []RedeemBatch
	q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows)
	b.core.OK(c, gin.H{"items": b.batchViews(rows), "total": total, "page": page, "page_size": pageSize})
}

// AdminCreateBatch POST /admin/membership/redeem/batches {name, plan_id, days, kind: cards|promo, count?, code?, max_uses?, expires_at?}
// cards：生成 count 个一次性卡密；promo：一个活动码（可自定义，默认随机），最多兑换 max_uses 次（每人一次）。
func (b *behavior) AdminCreateBatch(c *gin.Context) {
	var req struct {
		Name      string     `json:"name"`
		PlanID    uint       `json:"plan_id"`
		Days      int        `json:"days"`
		Kind      string     `json:"kind"`
		Count     int        `json:"count"`
		Code      string     `json:"code"`
		MaxUses   int        `json:"max_uses"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	req.Name = truncate(strings.TrimSpace(req.Name), 120)
	if req.Name == "" {
		b.core.Fail(c, http.StatusBadRequest, "请填写批次名称")
		return
	}
	if req.Days < 1 || req.Days > maxDurationDays {
		b.core.Fail(c, http.StatusBadRequest, errBadDuration.Error())
		return
	}
	var plan Plan
	if b.core.Gorm().First(&plan, req.PlanID).Error != nil {
		b.core.Fail(c, http.StatusBadRequest, errPlanNotFound.Error())
		return
	}
	if plan.Status != "active" {
		b.core.Fail(c, http.StatusBadRequest, errPlanArchived.Error())
		return
	}
	if req.ExpiresAt != nil && !req.ExpiresAt.After(time.Now()) {
		b.core.Fail(c, http.StatusBadRequest, "截止时间需晚于现在")
		return
	}
	batch := RedeemBatch{Name: req.Name, PlanID: plan.ID, Days: req.Days, Kind: req.Kind, ExpiresAt: req.ExpiresAt, Status: "active", CreatedBy: b.core.CurrentUser(c).ID}
	code := normalizeCode(req.Code)
	switch req.Kind {
	case "cards":
		if req.Count < 1 || req.Count > maxBatchCodes {
			b.core.Fail(c, http.StatusBadRequest, fmt.Sprintf("数量需在 1 到 %d 之间", maxBatchCodes))
			return
		}
		batch.MaxUses = 1
	case "promo":
		if req.MaxUses < 1 || req.MaxUses > maxCodeUses {
			b.core.Fail(c, http.StatusBadRequest, "可兑换次数需在 1 到 1000000 之间")
			return
		}
		if code != "" && (len(code) < 4 || len(code) > 40 || strings.IndexFunc(code, func(r rune) bool { return !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') }) >= 0) {
			b.core.Fail(c, http.StatusBadRequest, "自定义兑换码需为 4 到 40 位字母、数字或下划线")
			return
		}
		batch.MaxUses = req.MaxUses
	default:
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	err := b.core.Gorm().Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&batch).Error; err != nil {
			return err
		}
		if req.Kind == "promo" && code != "" {
			var exists int64
			tx.Model(&RedeemCode{}).Where("code = ?", code).Count(&exists)
			if exists > 0 {
				return errors.New("该兑换码已存在")
			}
			return tx.Create(&RedeemCode{BatchID: batch.ID, Code: code, MaxUses: batch.MaxUses}).Error
		}
		n := req.Count
		if req.Kind == "promo" {
			n = 1
		}
		_, err := createCodes(tx, batch, n, 0)
		return err
	})
	if err != nil {
		b.core.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	b.core.RecordAudit(c, "membership.redeem_batch_created", "membership", auditID(batch.ID), batch.Name,
		map[string]any{"changed_fields": []string{"redeem_batch"}, "plan": plan.Name, "kind": batch.Kind, "days": batch.Days})
	b.core.OK(c, b.batchViews([]RedeemBatch{batch})[0])
}

// AdminUpdateBatch PUT /admin/membership/redeem/batches/:id {status: active|disabled} 停用/启用整批。
func (b *behavior) AdminUpdateBatch(c *gin.Context) {
	var req struct {
		Status string `json:"status"`
	}
	if c.ShouldBindJSON(&req) != nil || (req.Status != "active" && req.Status != "disabled") {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	var batch RedeemBatch
	if b.core.Gorm().First(&batch, c.Param("id")).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "批次不存在")
		return
	}
	b.core.Gorm().Model(&batch).Update("status", req.Status)
	b.core.RecordAudit(c, "membership.redeem_batch_updated", "membership", auditID(batch.ID), batch.Name, map[string]any{"changed_fields": []string{"status"}})
	batch.Status = req.Status
	b.core.OK(c, b.batchViews([]RedeemBatch{batch})[0])
}

// AdminBatchCodes GET /admin/membership/redeem/batches/:id/codes?page=&page_size=&format=csv 批次中的兑换码（csv 导出全部）。
func (b *behavior) AdminBatchCodes(c *gin.Context) {
	var batch RedeemBatch
	if b.core.Gorm().First(&batch, c.Param("id")).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "批次不存在")
		return
	}
	q := b.core.Gorm().Model(&RedeemCode{}).Where("batch_id = ?", batch.ID)
	if c.Query("format") == "csv" {
		var codes []RedeemCode
		q.Order("id ASC").Find(&codes)
		var sb strings.Builder
		sb.WriteString("\ufeffcode,used,max_uses,disabled\n")
		for _, rc := range codes {
			sb.WriteString(fmt.Sprintf("%s,%d,%d,%t\n", formatCode(rc.Code), rc.UsedCount, rc.MaxUses, rc.Disabled))
		}
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=redeem-%d.csv", batch.ID))
		c.Data(http.StatusOK, "text/csv; charset=utf-8", []byte(sb.String()))
		b.core.RecordAudit(c, "membership.redeem_codes_exported", "membership", auditID(batch.ID), batch.Name, map[string]any{"changed_fields": []string{}})
		return
	}
	page, pageSize := b.core.Paginate(c)
	var total int64
	q.Count(&total)
	var codes []RedeemCode
	q.Order("id ASC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&codes)
	items := make([]gin.H, 0, len(codes))
	for _, rc := range codes {
		items = append(items, gin.H{"id": rc.ID, "code": formatCode(rc.Code), "used_count": rc.UsedCount, "max_uses": rc.MaxUses, "disabled": rc.Disabled})
	}
	b.core.OK(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

// AdminUpdateCode PUT /admin/membership/redeem/codes/:id {disabled} 停用/启用单个兑换码。
func (b *behavior) AdminUpdateCode(c *gin.Context) {
	var req struct {
		Disabled bool `json:"disabled"`
	}
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	var rc RedeemCode
	if b.core.Gorm().First(&rc, c.Param("id")).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "兑换码不存在")
		return
	}
	b.core.Gorm().Model(&rc).Update("disabled", req.Disabled)
	b.core.RecordAudit(c, "membership.redeem_code_updated", "membership", auditID(rc.ID), formatCode(rc.Code), map[string]any{"changed_fields": []string{"disabled"}})
	b.core.OK(c, gin.H{"id": rc.ID, "code": formatCode(rc.Code), "used_count": rc.UsedCount, "max_uses": rc.MaxUses, "disabled": req.Disabled})
}
