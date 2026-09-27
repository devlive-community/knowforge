// Package moderation 内容审核插件（Issue #87）：敏感词词典 + 发布前自动审查。
//   - 通过：直接发布，并记入「自动通过」列表供管理员抽查复审（复审驳回会撤回发布并通知作者）；
//   - 命中敏感词：拦截发布（章节保持草稿 / 书籍保持私有），记录命中位置（字段、行、列、上下文），通知作者与管理员；
//     管理员复审通过则自动发布，驳回则把命中位置与复审意见通知作者（作者可在「我的审核」查看）。
//
// 经 plugincore.RegisterPublishGuard 接入核心的发布路径，审核结论经 Core.ApplyModeration 落地；不依赖其他插件。
package moderation

import (
	"context"
	"encoding/json"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/i18ntext"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// 插件权限（启用时动态注册，禁用即移除）。
const (
	PermRead   authz.Permission = "moderation:read"   // 查看自己内容的审核记录
	PermManage authz.Permission = "moderation:manage" // 审核队列、敏感词词典与设置（仅管理员）
)

const (
	cfgEnabled       = "moderation_enabled"
	notificationType = "moderation" // 复用通知偏好中的「审核」开关
)

func init() {
	plugins.Register(plugins.Meta{
		Order:       110,
		Key:         plugins.KeyModeration,
		Name:        "发布审核",
		Description: "敏感词审核：章节发布与书籍公开前按敏感词词典自动审查。未命中直接发布并记入自动通过列表（管理员可复审撤回）；命中则拦截待人工审核，标出命中位置并通知作者与管理员，复审通过自动发布、驳回则把位置与意见通知作者。默认关闭。",
		Kind:        plugins.KindFeature,
		Builtin:     true,
		EnabledKey:  cfgEnabled,
		Models:      []any{&Word{}, &Case{}},
		Tables:      []string{"moderation_cases", "moderation_words"},
		AdminPerms:  []authz.Permission{PermManage},
		UserPerms:   []authz.Permission{PermRead},
	})
	plugincore.RegisterBehavior(&behavior{})
	plugincore.RegisterUserDataModels(&Case{})
	plugincore.RegisterPublishGuard(guard)
	plugincore.RegisterJob(aiReviewJob, func(core plugincore.Core) func(ctx context.Context, raw json.RawMessage) error {
		return (&behavior{core: core}).runAIReview
	})

	i18ntext.Register("notify.moderation.held", map[string]string{"zh-CN": "「{title}」包含待审核内容，已提交人工审核", "en": `"{title}" contains flagged content and is awaiting review`})
	i18ntext.Register("notify.moderation.pending", map[string]string{"zh-CN": "有新的内容待审核：{title}", "en": "New content awaiting review: {title}"})
	i18ntext.Register("notify.moderation.approved", map[string]string{"zh-CN": "「{title}」已通过审核并发布", "en": `"{title}" passed review and is now published`})
	i18ntext.Register("notify.moderation.rejected", map[string]string{"zh-CN": "「{title}」未通过审核：{note}", "en": `"{title}" did not pass review: {note}`})
	i18ntext.Register("notify.moderation.aiFlagged", map[string]string{"zh-CN": "「{title}」已自动通过，但被 AI 标记为疑似违规，请复审", "en": `"{title}" passed automatically but AI flagged it as a possible violation. Please review it`})
	i18ntext.Register("notify.moderation.passed", map[string]string{"zh-CN": "「{title}」已通过自动审核并发布", "en": `"{title}" passed automatic review and is now published`})
}
