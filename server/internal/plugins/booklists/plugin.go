// Package booklists 书单插件：用户把书籍整理成主题书单（每本书可附推荐语、可排序），公开书单出现在书单广场、
// 作者主页与所收录书籍的详情页，其他用户可以收藏书单。书单只展示查看者可读的书籍；私有书单只有创建者可见。
// 每人可创建的书单数为权益（booklists.max），可由会员方案与成长等级提升。
package booklists

import (
	"strconv"
	"time"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

const pluginKey = plugins.KeyBookLists

// PermUse 创建与管理自己的书单、收藏书单。
const PermUse authz.Permission = "booklists:use"

const (
	cfgEnabled   = "book_lists_enabled"
	cfgMaxBase   = "entitlement_booklists_max"
	entMax       = "booklists.max"
	defaultMax   = 20
	maxItems     = 500 // 每个书单最多收录的书籍数
	maxTitle     = 60
	maxDesc      = 1000
	maxNote      = 300
	coverSamples = 4 // 列表中每个书单展示的封面数
)

// List 一个书单。
type List struct {
	ID            uint   `gorm:"primaryKey" json:"id"`
	UserID        uint   `gorm:"index;not null" json:"user_id"`
	Title         string `gorm:"size:100;not null" json:"title"`
	Description   string `gorm:"type:text" json:"description"`
	IsPublic      bool   `gorm:"index" json:"is_public"`
	ItemCount     int    `gorm:"default:0" json:"item_count"`
	FollowerCount int    `gorm:"default:0;index" json:"follower_count"`
	// Featured 由管理员设为「精选」，展示在发现页（FeaturedAt 为设置时间，越新越靠前）
	Featured   bool       `gorm:"index;default:false" json:"featured"`
	FeaturedAt *time.Time `json:"featured_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `gorm:"index" json:"updated_at"`
}

func (List) TableName() string { return "book_lists" }

// Item 书单收录的一本书。
type Item struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	ListID    uint      `gorm:"not null;uniqueIndex:uk_book_list_item" json:"list_id"`
	BookID    uint      `gorm:"not null;index;uniqueIndex:uk_book_list_item" json:"book_id"`
	Note      string    `gorm:"size:1024" json:"note"`
	SortOrder int       `gorm:"default:0" json:"sort_order"`
	CreatedAt time.Time `json:"created_at"`
}

func (Item) TableName() string { return "book_list_items" }

// Follow 用户收藏的书单。
type Follow struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	ListID    uint      `gorm:"not null;index;uniqueIndex:uk_book_list_follow" json:"list_id"`
	UserID    uint      `gorm:"not null;uniqueIndex:uk_book_list_follow" json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
}

func (Follow) TableName() string { return "book_list_follows" }

func init() {
	plugins.Register(plugins.Meta{
		Order:       132,
		Key:         pluginKey,
		Name:        "书单",
		Description: "用户把书籍整理成主题书单，每本书可附推荐语并调整顺序；公开书单出现在书单广场、个人主页与所收录书籍的详情页，其他用户可以收藏。书单只展示查看者可读的书籍；每人的书单数为权益，可由会员/等级提升。默认关闭。",
		Kind:        plugins.KindFeature,
		Builtin:     true,
		EnabledKey:  cfgEnabled,
		Models:      []any{&List{}, &Item{}, &Follow{}},
		Tables:      []string{"book_list_follows", "book_list_items", "book_lists"},
		UserPerms:   []authz.Permission{PermUse},
		AdminPerms:  []authz.Permission{PermUse},
	})
	plugincore.RegisterBehavior(&behavior{})
	plugincore.RegisterUserDataModels(&List{}, &Follow{})
	plugincore.RegisterBookDataModels(&Item{})
	plugincore.RegisterEntitlement(plugincore.EntitlementDef{
		Key: entMax, Kind: plugincore.EntitlementLimit, Unit: "lists", Min: 0, Max: 10000, AllowUnlimited: true, Order: 34,
		Available: func(core plugincore.Core) bool { return core.PluginEnabled(pluginKey) },
		Base: func(core plugincore.Core) int64 {
			if v, err := strconv.ParseInt(core.GetSetting(cfgMaxBase), 10, 64); err == nil {
				return v
			}
			return defaultMax
		},
		SetBase: func(core plugincore.Core, v int64) error {
			return core.SetSetting(cfgMaxBase, strconv.FormatInt(v, 10), "权益：书单数量（基础）")
		},
	})
}

type behavior struct{ core plugincore.Core }

func (b *behavior) Key() string { return pluginKey }
