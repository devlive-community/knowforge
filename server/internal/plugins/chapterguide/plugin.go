// Package chapterguide 章节导读插件：为章节生成阅读前导读与本章要点，为书籍生成全书概览，分别显示在阅读页正文上方与书籍详情页。
// 作者在书籍设置中生成、编辑或开启「发布与修改后自动生成」；内容不变时复用已有结果，作者编辑过的不被自动覆盖。
// 模型经核心「AI 服务」调用并记录用量；费用由作者（计入其每月 AI 用量）或站点承担（管理员设置）；
// 每月生成次数为权益（chapterguide.monthly），可由会员方案或成长等级提升。
package chapterguide

import (
	"context"
	"encoding/json"
	"strconv"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/jobqueue"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

const pluginKey = plugins.KeyChapterGuide

// 插件权限（启用时动态注册，禁用即移除）。
const (
	PermUse    authz.Permission = "chapterguide:use"    // 为自己可编辑的书生成/编辑导读
	PermManage authz.Permission = "chapterguide:manage" // 插件设置（管理员）
)

const (
	cfgEnabled     = "chapter_guide_enabled"
	cfgCostBearer  = "chapter_guide_cost_bearer"
	cfgMonthlyBase = "chapter_guide_monthly"
	entMonthly     = "chapterguide.monthly"
	defaultMonthly = 200
	maxMonthly     = 100000

	bearerAuthor = "author"
	bearerSite   = "site"
)

func init() {
	plugincore.RegisterURLColumns("book_guide_overviews", "content") // 存储迁移：全书概览中的图片
	plugins.Register(plugins.Meta{
		Order:       126,
		Key:         pluginKey,
		Name:        "章节导读",
		Description: "AI 为章节生成阅读前导读与本章要点、为书籍生成全书概览，显示在阅读页与书籍详情页；作者可编辑，内容不变时复用，可开启发布与修改后自动生成。费用可由作者或站点承担；每月生成次数为权益，可由会员/等级提升。需先配置「AI 服务」，默认关闭。",
		Kind:        plugins.KindFeature,
		Builtin:     true,
		EnabledKey:  cfgEnabled,
		Models:      []any{&Guide{}, &Overview{}, &BookSetting{}, &Run{}},
		Tables:      []string{"chapter_guide_runs", "chapter_guide_book_settings", "book_guide_overviews", "chapter_guides"},
		AdminPerms:  []authz.Permission{PermManage},
		UserPerms:   []authz.Permission{PermUse},
	})
	b := &behavior{}
	plugincore.RegisterBehavior(b)
	plugincore.RegisterJob(jobChapter, func(core plugincore.Core) func(ctx context.Context, raw json.RawMessage) error {
		return (&behavior{core: core}).runChapter
	})
	plugincore.RegisterJob(jobOverview, func(core plugincore.Core) func(ctx context.Context, raw json.RawMessage) error {
		return (&behavior{core: core}).runOverview
	})
	plugincore.OnJobQueueSweep(func(core plugincore.Core, _ *jobqueue.Queue) {
		if core.PluginEnabled(pluginKey) {
			(&behavior{core: core}).sweepDirty()
		}
	})
	// 自动模式：章节首次发布立即生成；已发布章节修改后延迟生成（连续保存只生成一次）
	plugincore.OnChapterPublished(func(core plugincore.Core, book *models.Book, doc *models.Document) {
		gb := &behavior{core: core}
		if core.PluginEnabled(pluginKey) && gb.autoEnabled(book.ID) {
			_ = gb.enqueueChapter(book.ID, doc.ID, book.UserID, false)
		}
	})
	plugincore.OnChapterContentChanged(func(core plugincore.Core, book *models.Book, doc *models.Document) {
		gb := &behavior{core: core}
		if core.PluginEnabled(pluginKey) && doc.Status == "published" && gb.autoEnabled(book.ID) {
			gb.scheduleAuto(book, doc)
		}
	})
	plugincore.RegisterEntitlement(plugincore.EntitlementDef{
		Key: entMonthly, Kind: plugincore.EntitlementLimit, Unit: "uses", Min: 0, Max: maxMonthly, AllowUnlimited: true, Order: 86,
		Available: func(core plugincore.Core) bool { return core.PluginEnabled(pluginKey) },
		Base: func(core plugincore.Core) int64 {
			if v, err := strconv.ParseInt(core.GetSetting(cfgMonthlyBase), 10, 64); err == nil && (v == plugincore.Unlimited || (v >= 0 && v <= maxMonthly)) {
				return v
			}
			return defaultMonthly
		},
		SetBase: func(core plugincore.Core, v int64) error {
			return core.SetSetting(cfgMonthlyBase, strconv.FormatInt(v, 10), "章节导读：每月生成次数（基础）")
		},
	})
}

type behavior struct{ core plugincore.Core }

func (b *behavior) Key() string { return pluginKey }

// costBearer 费用承担方：author（默认，计入作者的每月 AI 用量）或 site（记为系统调用）。
func (b *behavior) costBearer() string {
	if b.core.GetSetting(cfgCostBearer) == bearerSite {
		return bearerSite
	}
	return bearerAuthor
}
