package membership

import (
	"encoding/json"
	"time"

	"knowforge/server/internal/models"
)

// Plan 会员方案：名称/说明为默认语言缓存（多语言经可翻译资源 membership_plan 存取），权益为方案有效期内生效的取值。
type Plan struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Name        string `gorm:"size:120" json:"name"`
	Description string `gorm:"size:500" json:"description"`
	IconType    string `gorm:"size:10;default:'fa'" json:"icon_type"` // fa | image | svg
	IconValue   string `gorm:"size:500;default:'fa-crown'" json:"icon_value"`
	Color       string `gorm:"size:20;default:''" json:"color"`
	// Entitlements 会员有效期内的权益（独占：未设置的键回退基础值，不叠加成长等级）
	Entitlements models.EntitlementMap `gorm:"type:text" json:"entitlements"`
	Status       string                `gorm:"size:20;default:'active';index" json:"status"` // active | archived（归档后不再开通/售卖，已有会员不受影响）
	SortOrder    int                   `gorm:"default:0" json:"sort_order"`
	TrialDays    int                   `gorm:"default:0" json:"trial_days"` // 免费试用天数（0 为不提供试用）
	// GroupEnabled 可作为团队会员购买（按席位计价：每个席位按该方案的价格）
	GroupEnabled bool      `gorm:"default:false" json:"group_enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`

	Prices       []Price         `gorm:"foreignKey:PlanID" json:"prices"`
	Translations json.RawMessage `gorm:"-" json:"translations,omitempty"`
}

func (Plan) TableName() string { return "membership_plans" }

// Price 方案的一档时长价格（金额以最小货币单位存储，如分）。
type Price struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	PlanID             uint      `gorm:"index;not null" json:"plan_id"`
	DurationDays       int       `gorm:"not null" json:"duration_days"`
	PriceCents         int64     `gorm:"not null" json:"price_cents"`
	OriginalPriceCents int64     `gorm:"default:0" json:"original_price_cents"` // 划线价，0 表示不显示
	SortOrder          int       `gorm:"default:0" json:"sort_order"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

func (Price) TableName() string { return "membership_prices" }

// UserMembership 用户当前的会员（每人一条）：同方案续期顺延到期时间；更换方案从当前时间重新计算。
type UserMembership struct {
	UserID    uint      `gorm:"primaryKey" json:"user_id"`
	PlanID    uint      `gorm:"index;not null" json:"plan_id"`
	StartedAt time.Time `json:"started_at"`
	ExpiresAt time.Time `gorm:"index" json:"expires_at"`
	Trial     bool      `gorm:"default:false" json:"trial"` // 试用中（购买、兑换或管理员调整后不再是试用）
	// RemindedAt / ExpiredNoticeAt 到期提醒、到期通知的发送时间；有效期变化时清空以便下一周期重新提醒
	RemindedAt      *time.Time `json:"-"`
	ExpiredNoticeAt *time.Time `json:"-"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (UserMembership) TableName() string { return "user_memberships" }

// 会员流水动作。
const (
	ActionGrant  = "grant"  // 新开通（或到期后重新开通）
	ActionExtend = "extend" // 同方案续期
	ActionSwitch = "switch" // 有效期内更换方案
	ActionAdjust = "adjust" // 管理员直接调整方案/到期时间
	ActionRevoke = "revoke" // 取消
)

// Record 会员流水：开通/续期/更换/调整/取消（方案名快照，方案删除后仍可展示）。
type Record struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	UserID        uint       `gorm:"index;not null" json:"user_id"`
	PlanID        uint       `json:"plan_id"`
	PlanName      string     `gorm:"size:120" json:"plan_name"`
	Action        string     `gorm:"size:20" json:"action"`
	Days          int        `json:"days"`
	PrevExpiresAt *time.Time `json:"prev_expires_at"`
	ExpiresAt     *time.Time `json:"expires_at"`
	Source        string     `gorm:"size:20" json:"source"`     // admin | 其他插件登记的来源（如 order）
	SourceRef     string     `gorm:"size:64" json:"source_ref"` // 来源引用（如订单号）
	OperatorID    uint       `json:"operator_id"`
	Reason        string     `gorm:"size:255" json:"reason"`
	CreatedAt     time.Time  `json:"created_at"`
}

func (Record) TableName() string { return "membership_records" }
