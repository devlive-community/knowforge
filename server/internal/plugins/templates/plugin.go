// Package templates 模板插件：章节模板（写作时插入到光标处）与书籍模板（新建书籍时按模板生成章节目录与正文）。
// 模板分两类：站点模板由管理员维护（插件首次启用时预置几份常用模板），所有作者可用；个人模板只有自己可见，
// 可以从零编写，也可以把当前章节或自己的书籍保存为模板。模板正文可使用变量（{{date}}、{{book}}、{{chapter}} 等），
// 使用时替换为实际值。每人可保存的个人模板数为权益（templates.max），可由会员方案与成长等级提升。
package templates

import (
	"strconv"
	"time"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

const pluginKey = plugins.KeyTemplates

const (
	// PermUse 使用模板、管理自己的个人模板。
	PermUse authz.Permission = "templates:use"
	// PermManage 管理站点模板。
	PermManage authz.Permission = "templates:manage"
)

const (
	cfgEnabled = "templates_enabled"
	cfgMaxBase = "entitlement_templates_max"
	entMax     = "templates.max"
	defaultMax = 50

	KindChapter = "chapter"
	KindBook    = "book"

	maxTitle        = 100
	maxDesc         = 500
	maxContentBytes = 200 << 10 // 单章正文上限
	maxTotalBytes   = 2 << 20   // 书籍模板全部正文合计上限
	maxNodes        = 300       // 书籍模板最多章节数
	maxDepth        = 5         // 书籍模板最大层级
)

// Template 一份模板。UserID 为 0 表示站点模板（不随任何账号删除）。
type Template struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	UserID      uint   `gorm:"index;not null;default:0" json:"user_id"`
	Official    bool   `gorm:"index" json:"official"`
	Kind        string `gorm:"size:16;index;not null" json:"kind"`
	Title       string `gorm:"size:200;not null" json:"title"`
	Description string `gorm:"size:1024" json:"description"`
	// Content 章节模板的正文（Markdown）
	Content string `gorm:"type:text" json:"content,omitempty"`
	// Chapters 书籍模板的章节目录（JSON：[{title, content, children}]）
	Chapters     string    `gorm:"type:text" json:"-"`
	ChapterCount int       `gorm:"default:0" json:"chapter_count"`
	UseCount     int       `gorm:"default:0" json:"use_count"`
	SortOrder    int       `gorm:"default:0" json:"sort_order"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (Template) TableName() string { return "content_templates" }

func init() {
	plugins.Register(plugins.Meta{
		Order:       134,
		Key:         pluginKey,
		Name:        "模板",
		Description: "新建书籍或写章节时可以选用模板：书籍模板一次生成章节目录与正文，章节模板插入到写作台光标处。站点模板由管理员维护（首次启用时预置常用模板），作者也可以把章节或自己的书保存为个人模板；模板支持日期、书名、章节名等变量。每人的个人模板数为权益，可由会员/等级提升。默认关闭。",
		Kind:        plugins.KindFeature,
		Builtin:     true,
		EnabledKey:  cfgEnabled,
		Models:      []any{&Template{}},
		Tables:      []string{"content_templates"},
		UserPerms:   []authz.Permission{PermUse},
		AdminPerms:  []authz.Permission{PermUse, PermManage},
	})
	plugincore.RegisterBehavior(&behavior{})
	plugincore.RegisterUserDataModels(&Template{})
	plugincore.OnPluginEnabled(pluginKey, seedOfficialTemplates)
	plugincore.RegisterEntitlement(plugincore.EntitlementDef{
		Key: entMax, Kind: plugincore.EntitlementLimit, Unit: "templates", Min: 0, Max: 10000, AllowUnlimited: true, Order: 35,
		Available: func(core plugincore.Core) bool { return core.PluginEnabled(pluginKey) },
		Base: func(core plugincore.Core) int64 {
			if v, err := strconv.ParseInt(core.GetSetting(cfgMaxBase), 10, 64); err == nil {
				return v
			}
			return defaultMax
		},
		SetBase: func(core plugincore.Core, v int64) error {
			return core.SetSetting(cfgMaxBase, strconv.FormatInt(v, 10), "权益：个人模板数量（基础）")
		},
	})
}

type behavior struct{ core plugincore.Core }

func (b *behavior) Key() string { return pluginKey }
