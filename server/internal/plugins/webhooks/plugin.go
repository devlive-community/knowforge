// Package webhooks Webhook 插件：用户订阅自己书籍上的事件（章节发布、收到评论、收到点赞或收藏），
// 事件发生时向用户填写的地址投递带签名的 JSON。投递经任务队列异步执行，失败按退避重试，
// 每次投递记录响应码、响应片段与耗时，可重新投递；连续失败过多的订阅自动停用并通知用户。
// 默认拒绝投递到内网与本机地址（防止 SSRF），内网部署可用环境变量 KNOWFORGE_WEBHOOK_ALLOW_PRIVATE=true 放开。
// 每人的订阅数为权益（webhooks.max），可由会员方案与成长等级提升。
package webhooks

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/i18ntext"
	"knowforge/server/internal/jobqueue"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

const pluginKey = plugins.KeyWebhooks

// PermUse 管理自己的 Webhook 订阅。
const PermUse authz.Permission = "webhooks:use"

const (
	cfgEnabled      = "webhooks_enabled"
	cfgMaxBase      = "entitlement_webhooks_max"
	entMax          = "webhooks.max"
	defaultMax      = 5
	jobDeliver      = "webhook.deliver"
	maxAttempts     = 5  // 每次投递最多尝试次数（含首次）
	disableAfter    = 10 // 连续投递失败（用尽重试）多少次后自动停用
	deliveryKeepFor = 30 * 24 * time.Hour
)

// 可订阅的事件。
const (
	EventChapterPublished = "chapter.published"
	EventCommentReceived  = "comment.received"
	EventReactionReceived = "reaction.received"
	EventSaleCompleted    = "sale.completed"     // 作品被购买（付费内容插件）
	EventQuestionReceived = "question.received"  // 书籍收到公开提问（书籍问答插件）
	EventMembership       = "membership.changed" // 自己的会员开通、续期、调整、取消或到期（会员插件）
	EventPing             = "ping"
)

// Events 可订阅的事件（有序，供前端展示）。
var Events = []string{EventChapterPublished, EventCommentReceived, EventReactionReceived, EventSaleCompleted, EventQuestionReceived, EventMembership}

// Hook 一个 Webhook 订阅。
type Hook struct {
	ID       uint     `gorm:"primaryKey" json:"id"`
	UserID   uint     `gorm:"index;not null" json:"user_id"`
	URL      string   `gorm:"size:500" json:"url"`
	Secret   string   `gorm:"size:80" json:"-"` // 签名密钥（只在创建与重置时返回）
	Events   []string `gorm:"type:text;serializer:json" json:"events"`
	BookID   uint     `gorm:"index;default:0" json:"book_id"` // 0 为我的全部书籍
	Active   bool     `json:"active"`
	Failures int      `json:"failures"` // 连续失败的投递数
	// DisabledReason 自动停用的原因（用户重新启用时清空）
	DisabledReason string     `gorm:"size:255" json:"disabled_reason"`
	LastDeliveryAt *time.Time `json:"last_delivery_at"`
	LastStatus     string     `gorm:"size:10" json:"last_status"` // success | failed
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (Hook) TableName() string { return "webhooks" }

// Delivery 一次投递（含重试）。
type Delivery struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	HookID         uint       `gorm:"index;not null" json:"hook_id"`
	Event          string     `gorm:"size:40" json:"event"`
	Payload        string     `gorm:"type:text" json:"payload"`
	Status         string     `gorm:"size:10;index" json:"status"` // pending | success | failed
	Attempts       int        `json:"attempts"`
	ResponseStatus int        `json:"response_status"`
	ResponseBody   string     `gorm:"size:1024" json:"response_body"`
	Error          string     `gorm:"size:500" json:"error"`
	DurationMs     int64      `json:"duration_ms"`
	CreatedAt      time.Time  `gorm:"index" json:"created_at"`
	DeliveredAt    *time.Time `json:"delivered_at"`
}

func (Delivery) TableName() string { return "webhook_deliveries" }

func init() {
	plugins.Register(plugins.Meta{
		Order:       128,
		Key:         pluginKey,
		Name:        "Webhook",
		Description: "用户订阅自己书籍上的事件（章节发布、收到评论、收到点赞或收藏），事件发生时向指定地址投递带签名的 JSON；失败自动重试，可查看投递记录并重新投递。默认拒绝投递到内网地址；订阅数为权益，可由会员/等级提升。默认关闭。",
		Kind:        plugins.KindFeature,
		Builtin:     true,
		EnabledKey:  cfgEnabled,
		Models:      []any{&Hook{}, &Delivery{}},
		Tables:      []string{"webhook_deliveries", "webhooks"},
		UserPerms:   []authz.Permission{PermUse},
		AdminPerms:  []authz.Permission{PermUse},
	})
	plugincore.RegisterBehavior(&behavior{})
	plugincore.RegisterUserDataModels(&Hook{})
	plugincore.RegisterJob(jobDeliver, func(core plugincore.Core) func(ctx context.Context, raw json.RawMessage) error {
		return (&behavior{core: core}).deliver
	})
	plugincore.OnJobQueueSweep(func(core plugincore.Core, _ *jobqueue.Queue) {
		if core.Gorm().Migrator().HasTable(&Delivery{}) {
			core.Gorm().Where("created_at < ?", time.Now().Add(-deliveryKeepFor)).Delete(&Delivery{})
		}
	})
	plugincore.RegisterEntitlement(plugincore.EntitlementDef{
		Key: entMax, Kind: plugincore.EntitlementLimit, Unit: "hooks", Min: 0, Max: 1000, AllowUnlimited: true, Order: 33,
		Available: func(core plugincore.Core) bool { return core.PluginEnabled(pluginKey) },
		Base: func(core plugincore.Core) int64 {
			if v, err := strconv.ParseInt(core.GetSetting(cfgMaxBase), 10, 64); err == nil {
				return v
			}
			return defaultMax
		},
		SetBase: func(core plugincore.Core, v int64) error {
			return core.SetSetting(cfgMaxBase, strconv.FormatInt(v, 10), "权益：Webhook 订阅数（基础）")
		},
	})

	// 事件来源
	plugincore.OnChapterPublished(func(core plugincore.Core, book *models.Book, doc *models.Document) {
		b := &behavior{core: core}
		b.emit(book.UserID, book.ID, EventChapterPublished, map[string]any{"book": b.bookInfo(book), "chapter": b.chapterInfo(book, doc)})
	})
	plugincore.OnActivity(func(core plugincore.Core, ev plugincore.ActivityEvent) {
		b := &behavior{core: core}
		switch ev.Type {
		case "comment.received":
			b.onComment(ev)
		case "reaction.received":
			b.onReaction(ev)
		case "paid.sold":
			b.onSale(ev)
		case "qa.question_received":
			b.onQuestion(ev)
		case "membership.changed":
			b.emit(ev.UserID, 0, EventMembership, ev.Data)
		}
	})

	i18ntext.Register("notify.webhooks.disabled", map[string]string{
		"zh-CN": "Webhook 已自动停用：投递到 {url} 连续失败 {n} 次，请检查后在「Webhook」中重新启用",
		"en":    "A webhook was turned off after {n} failed deliveries to {url}. Check it and turn it back on under Webhooks",
	})
}

type behavior struct{ core plugincore.Core }

func (b *behavior) Key() string { return pluginKey }
