// Package readaloud AI 朗读插件：读者在阅读页用语音合成收听章节，逐段播放并高亮当前段落。
// 段落由阅读页按实际排版切分后逐段请求；服务端只合成「确实出自该章节、读者有权阅读」的文字（付费章节需已解锁），
// 防止被当作通用语音合成接口。合成经核心「AI 服务」的语音合成并按字符数记录用量；相同文字与音色的音频缓存在
// 数据目录（私有，不进对象存储与备份）复用。每月朗读字数为权益（readaloud.monthly_chars），同一段文字当月重复收听不重复计数。
package readaloud

import (
	"strconv"
	"time"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

const pluginKey = plugins.KeyReadAloud

const (
	// PermUse 在阅读页收听朗读。
	PermUse authz.Permission = "readaloud:use"
	// PermManage 插件设置与缓存管理（管理员）。
	PermManage authz.Permission = "readaloud:manage"
)

const (
	cfgEnabled      = "read_aloud_enabled"
	cfgMonthlyBase  = "entitlement_readaloud_monthly_chars"
	cfgCacheMaxMB   = "read_aloud_cache_max_mb"
	entMonthlyChars = "readaloud.monthly_chars"

	defaultMonthlyChars = 20000
	maxMonthlyChars     = 100_000_000
	defaultCacheMaxMB   = 2048
	maxCacheMaxMB       = 1 << 20

	maxSegmentRunes = 800 // 单段最多字数（阅读页按句切分为不超过 500 字的段落）
	featureSpeech   = "readaloud.speech"
	cacheDirName    = "read-aloud-cache"
)

// Play 读者当月收听过的段落（按文字摘要去重）：用于统计每月朗读字数，同一段当月重复收听不重复计数。
type Play struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"uniqueIndex:idx_read_aloud_play;not null" json:"user_id"`
	Period    string    `gorm:"size:7;uniqueIndex:idx_read_aloud_play;not null" json:"period"` // 2006-01
	TextHash  string    `gorm:"size:64;uniqueIndex:idx_read_aloud_play;not null" json:"-"`
	DocID     uint      `gorm:"index" json:"doc_id"`
	Chars     int       `json:"chars"`
	CreatedAt time.Time `json:"created_at"`
}

func (Play) TableName() string { return "read_aloud_plays" }

func init() {
	plugins.Register(plugins.Meta{
		Order:       135,
		Key:         pluginKey,
		Name:        "AI 朗读",
		Description: "读者在阅读页用 AI 语音收听章节，逐段播放并高亮当前段落，可调语速、切换音色，读完自动进入下一章。只朗读读者有权阅读的内容（付费章节需解锁）；已合成的音频缓存复用。每月朗读字数为权益，可由会员/等级提升。需先在「AI 服务」中配置语音合成，默认关闭。",
		Kind:        plugins.KindFeature,
		Builtin:     true,
		EnabledKey:  cfgEnabled,
		Models:      []any{&Play{}},
		Tables:      []string{"read_aloud_plays"},
		UserPerms:   []authz.Permission{PermUse},
		AdminPerms:  []authz.Permission{PermUse, PermManage},
	})
	plugincore.RegisterBehavior(&behavior{})
	plugincore.RegisterUserDataModels(&Play{})
	plugincore.RegisterEntitlement(plugincore.EntitlementDef{
		Key: entMonthlyChars, Kind: plugincore.EntitlementLimit, Unit: "chars", Min: 0, Max: maxMonthlyChars, AllowUnlimited: true, Order: 87,
		Available: func(core plugincore.Core) bool {
			model, _ := core.AISpeechInfo()
			return core.PluginEnabled(pluginKey) && model != ""
		},
		Base: func(core plugincore.Core) int64 {
			if v, err := strconv.ParseInt(core.GetSetting(cfgMonthlyBase), 10, 64); err == nil && (v == plugincore.Unlimited || (v >= 0 && v <= maxMonthlyChars)) {
				return v
			}
			return defaultMonthlyChars
		},
		SetBase: func(core plugincore.Core, v int64) error {
			return core.SetSetting(cfgMonthlyBase, strconv.FormatInt(v, 10), "权益：每月朗读字数（基础）")
		},
	})
}

type behavior struct{ core plugincore.Core }

func (b *behavior) Key() string { return pluginKey }
