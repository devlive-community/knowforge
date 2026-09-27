// Package qa 问答插件：针对某本书的专业问答。
//   - AI 问答：书籍内容分块建索引（关键词 + 向量混合检索），大模型只依据检索到的片段回答并标注出处（章节 › 小节，可跳转）；
//   - Agent 模式：大模型通过工具（检索全书、阅读小节、查看目录）多步查找后作答，适合跨章节的复杂问题；
//   - 划词提问：阅读页选中文字直接提问，结合所在章节回答；
//   - 社区问答：读者提问、作者与读者回答、采纳最佳答案，沉淀为本书常见问题。
//
// AI 能力经 plugincore.Core 的 AIChat / AIEmbed 调用站点「AI 服务」；检索时经内容门禁过滤读者无权阅读的章节（如未解锁的付费内容）。
// 每日 AI 提问次数为权益（qa.ai_daily），可由会员方案或成长等级提升。
package qa

import (
	"context"
	"encoding/json"
	"strconv"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/i18ntext"
	"knowforge/server/internal/jobqueue"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// 插件权限（启用时动态注册，禁用即移除）。
const (
	PermUse    authz.Permission = "qa:use"    // 提问、回答、使用 AI 问答
	PermManage authz.Permission = "qa:manage" // 问答设置（仅管理员）
)

const (
	cfgEnabled      = "qa_enabled"
	entAIDaily      = "qa.ai_daily"
	defaultAIDaily  = 20
	entAgentDaily   = "qa.agent_daily"
	defaultAgentDay = 5
	indexJobType    = "qa.index"
	maxQuestionLen  = 1000
	maxSelectionLen = 2000
)

func init() {
	plugins.Register(plugins.Meta{
		Order:       120,
		Key:         plugins.KeyQA,
		Name:        "问答",
		Description: "书籍专业问答：AI 基于本书内容回答并标注出处（关键词 + 向量混合检索，支持 Agent 多步查找与阅读页划词提问），以及读者提问、回答与采纳的社区问答。AI 能力需先在系统设置配置「AI 服务」；每日 AI 提问次数为权益，可由会员/等级提升。默认关闭。",
		Kind:        plugins.KindFeature,
		Builtin:     true,
		EnabledKey:  cfgEnabled,
		Models:      []any{&Chunk{}, &IndexState{}, &Ask{}, &Question{}, &Answer{}, &BookVector{}, &DocVector{}, &InsightDigest{}},
		Tables:      []string{"qa_insight_digests", "qa_doc_vectors", "qa_book_vectors", "qa_answers", "qa_questions", "qa_asks", "qa_index_states", "qa_chunks"},
		AdminPerms:  []authz.Permission{PermManage},
		UserPerms:   []authz.Permission{PermUse},
	})
	plugincore.RegisterBehavior(&behavior{})
	plugincore.RegisterUserDataModels(&Ask{}, &Question{}, &Answer{})
	plugincore.OnJobQueueSweep(func(core plugincore.Core, _ *jobqueue.Queue) {
		if core.PluginEnabled(plugins.KeyQA) {
			sweepInterruptedAsks(core)
			purgeExpiredTraces(core)
			sweepInsights(core)
			if b := (&behavior{core: core}); b.semanticAvailable() {
				b.enqueueStaleIndexes(context.Background())
			}
		}
	})
	registerSemantic()
	plugincore.RegisterJob(indexJobType, func(core plugincore.Core) func(ctx context.Context, raw json.RawMessage) error {
		return (&behavior{core: core}).runEmbedJob
	})
	plugincore.RegisterEntitlement(plugincore.EntitlementDef{
		Key: entAIDaily, Kind: plugincore.EntitlementLimit, Unit: "questions", Min: 0, Max: 10000, AllowUnlimited: true, Order: 80,
		Available: func(core plugincore.Core) bool { return core.PluginEnabled(plugins.KeyQA) },
		Base: func(core plugincore.Core) int64 {
			if v, err := strconv.ParseInt(core.GetSetting("qa_ai_daily"), 10, 64); err == nil && (v == plugincore.Unlimited || (v >= 0 && v <= 10000)) {
				return v
			}
			return defaultAIDaily
		},
		SetBase: func(core plugincore.Core, v int64) error {
			return core.SetSetting("qa_ai_daily", strconv.FormatInt(v, 10), "问答：每日 AI 提问次数（基础）")
		},
	})

	// 深度模式（Agent，一次提问多次调用模型）单独计次：0 表示不可用，可作为会员专享
	plugincore.RegisterEntitlement(plugincore.EntitlementDef{
		Key: entAgentDaily, Kind: plugincore.EntitlementLimit, Unit: "questions", Min: 0, Max: 10000, AllowUnlimited: true, Order: 81,
		Available: func(core plugincore.Core) bool { return core.PluginEnabled(plugins.KeyQA) },
		Base: func(core plugincore.Core) int64 {
			if v, err := strconv.ParseInt(core.GetSetting("qa_agent_daily"), 10, 64); err == nil && (v == plugincore.Unlimited || (v >= 0 && v <= 10000)) {
				return v
			}
			return defaultAgentDay
		},
		SetBase: func(core plugincore.Core, v int64) error {
			return core.SetSetting("qa_agent_daily", strconv.FormatInt(v, 10), "问答：每日深度模式提问次数（基础）")
		},
	})

	// 语义搜索：能否使用为权益（基础为开，可设为会员专享；相关推荐不受限）
	plugincore.RegisterEntitlement(plugincore.EntitlementDef{
		Key: entSemanticSearch, Kind: plugincore.EntitlementFlag, Order: 83,
		Available: func(core plugincore.Core) bool { return (&behavior{core: core}).semanticAvailable() },
		Base: func(core plugincore.Core) int64 {
			if core.GetSetting("qa_semantic_search_base") == "0" {
				return 0
			}
			return 1
		},
		SetBase: func(core plugincore.Core, v int64) error {
			return core.SetSetting("qa_semantic_search_base", strconv.FormatInt(v, 10), "问答：语义搜索（基础）")
		},
	})
	// 作者洞察：能否查看为权益（基础为开，可设为会员专享）
	plugincore.RegisterEntitlement(plugincore.EntitlementDef{
		Key: entInsights, Kind: plugincore.EntitlementFlag, Order: 82,
		Available: func(core plugincore.Core) bool { return core.PluginEnabled(plugins.KeyQA) },
		Base: func(core plugincore.Core) int64 {
			if core.GetSetting("qa_insights_base") == "0" {
				return 0
			}
			return 1
		},
		SetBase: func(core plugincore.Core, v int64) error {
			return core.SetSetting("qa_insights_base", strconv.FormatInt(v, 10), "问答：读者问题洞察（基础）")
		},
	})
	i18ntext.Register("notify.qa.weeklyInsights", map[string]string{
		"zh-CN": "《{book}》本周读者提问：AI 提问 {asks} 个，社区新提问 {questions} 个，尚未回答 {open} 个",
		"en":    `This week in "{book}": {asks} AI questions, {questions} new community questions, {open} still unanswered`,
	})
	i18ntext.Register("notify.qa.answered", map[string]string{"zh-CN": "你的提问「{title}」有了新回答", "en": `Your question "{title}" has a new answer`})
	i18ntext.Register("notify.qa.accepted", map[string]string{"zh-CN": "你的回答被采纳：{title}", "en": `Your answer was accepted: {title}`})
	i18ntext.Register("notify.qa.asked", map[string]string{"zh-CN": "《{book}》有新的读者提问：{title}", "en": `New reader question in "{book}": {title}`})
}
