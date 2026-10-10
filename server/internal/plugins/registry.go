// Package plugins 保存各内置插件的「元数据」定义，与核心 app 包解耦：
// 每个插件在自己的子目录（internal/plugins/<name>/）里通过 init() 调用 Register 自注册，
// app 包只读取 All() 得到清单（禁止把「有哪些插件」写死在 app 里），便于后续插件商店化。
//
// 说明：本包只承载声明式元数据（键、名称、独占表/权限、启用配置键、展示顺序等）；插件的「行为」
// （路由、后台任务、事件订阅、启用钩子）在各子包内实现，经 internal/plugincore 的 Core 接口与钩子
// 访问核心能力，不依赖 app 包。
package plugins

import (
	"sort"

	"knowforge/server/internal/authz"
)

// 插件类型。
const (
	KindRuntime = "runtime" // 需下载运行时依赖（二进制/镜像）
	KindFeature = "feature" // 纯功能开关
)

// 内置插件键（单一事实来源；app 包的同名常量引用这里）。
const (
	KeyPDFExport        = "pdf-export"
	KeyAchievements     = "achievements"
	KeyBookTranslations = "book-translations"
	KeyBookVersions     = "book-versions"
	KeyWatermark        = "watermark"
	KeyBookFollow       = "book-follow"
	KeyGrowth           = "growth"
	KeyContentCollect   = "content-collect"
	KeyTags             = "tags"
	KeyMembership       = "membership"
	KeyPayment          = "payment"
	KeyModeration       = "moderation"
	KeyPaidContent      = "paid-content"
	KeyQA               = "qa"
	KeyAIWriter         = "ai-writer"
	KeyChapterGuide     = "chapter-guide"
	KeyWebhooks         = "webhooks"
	KeyFeeds            = "feeds"
	KeyEmbed            = "embed"
	KeyBacklinks        = "backlinks"
	KeyBookLists        = "book-lists"
	KeyTemplates        = "templates"
	KeyReadAloud        = "read-aloud"
	KeyTeams            = "teams"
	KeyIndexNow         = "indexnow"
	KeyCategories       = "categories"
)

// Meta 一个插件的声明式元数据（不含依赖 app 的行为）。
type Meta struct {
	Key         string             `json:"key"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	SizeHint    string             `json:"size_hint"`
	Kind        string             `json:"kind"`
	Builtin     bool               `json:"builtin"`
	Order       int                `json:"-"` // 稳定展示顺序（越小越靠前）
	EnabledKey  string             `json:"-"` // feature 插件复用的站点配置开关键（空则以 Plugin.Installed 记录）
	Models      []any              `json:"-"` // 独占表：启用时 AutoMigrate
	Tables      []string           `json:"-"` // 独占表名：purge 卸载时 DROP
	AdminPerms  []authz.Permission `json:"-"` // 启用时授予管理员的权限
	UserPerms   []authz.Permission `json:"-"` // 启用时授予普通用户的权限
}

var registry []Meta

// Register 供各插件子包在 init() 中自注册。
func Register(m Meta) {
	registry = append(registry, m)
}

// All 返回按 Order 稳定排序的插件清单副本。
func All() []Meta {
	out := make([]Meta, len(registry))
	copy(out, registry)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}
