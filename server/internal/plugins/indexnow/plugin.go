// Package indexnow submits changed public page URLs through the IndexNow protocol.
package indexnow

import (
	"context"
	"encoding/json"
	"time"

	"knowforge/server/internal/jobqueue"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

const (
	pluginKey       = plugins.KeyIndexNow
	cfgEnabled      = "indexnow_enabled"
	jobSubmit       = "indexnow.submit"
	maxAttempts     = 5
	maxBatchURLs    = 1000
	staleClaimAfter = 5 * time.Minute
	submissionKeep  = 90 * 24 * time.Hour
)

// Config is private plugin data. The key is public only through the verified root key file.
type Config struct {
	ID  uint   `gorm:"primaryKey" json:"-"`
	Key string `gorm:"size:128;not null" json:"-"`
	// 访问时推送：公开书籍详情 / 章节被访问时立即推送该页，同一地址在 VisitIntervalHours 内只推一次
	VisitPush          bool `gorm:"not null;default:false" json:"-"`
	VisitBooks         bool `gorm:"not null;default:false" json:"-"`
	VisitChapters      bool `gorm:"not null;default:false" json:"-"`
	VisitIntervalHours int  `gorm:"not null;default:24" json:"-"`
	// TriggerToken 触发地址的密钥：访问 /indexnow/trigger?token= 立即推送积压的 URL；为空表示未开启
	TriggerToken string    `gorm:"size:64;not null;default:''" json:"-"`
	UpdatedAt    time.Time `json:"-"`
}

func (Config) TableName() string { return "indexnow_config" }

// URL stores the latest unsubmitted generation for a public URL. Repeated changes
// coalesce by URL while Version keeps updates arriving during a send from being lost.
type URL struct {
	ID               uint       `gorm:"primaryKey" json:"-"`
	Address          string     `gorm:"type:text;not null" json:"-"`
	AddressHash      string     `gorm:"size:64;uniqueIndex;not null" json:"-"`
	Version          int64      `gorm:"not null;default:1" json:"-"`
	SubmittedVersion int64      `gorm:"not null;default:0" json:"-"`
	LockedAt         *time.Time `gorm:"index" json:"-"`
	// ClaimToken 本次认领的随机令牌：认领后的更新按令牌匹配，不依赖 locked_at 的精度
	// （MySQL / PostgreSQL 会截断纳秒，按时间相等匹配会落空，导致已推送的 URL 一直处于待推送并被反复提交）。
	ClaimToken      string     `gorm:"size:32;index;not null;default:''" json:"-"`
	LastSubmittedAt *time.Time `json:"-"`
	Attempts        int        `gorm:"not null;default:0" json:"-"`
	LastError       string     `gorm:"size:1000" json:"-"`
	UpdatedAt       time.Time  `gorm:"index" json:"-"`
}

func (URL) TableName() string { return "indexnow_urls" }

// PushLog 一次推送的记录（队列、访问、触发地址或手动），只保留最近 keepLogs 条，供管理页确认推送结果。
type PushLog struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Source    string    `gorm:"size:16;not null" json:"source"` // queue | visit | trigger | manual
	URLCount  int       `gorm:"not null" json:"url_count"`
	SampleURL string    `gorm:"size:500" json:"sample_url"`
	Status    string    `gorm:"size:16;not null" json:"status"` // ok | error
	Message   string    `gorm:"size:500" json:"message"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`
}

func (PushLog) TableName() string { return "indexnow_logs" }

type submitPayload struct{}

type behavior struct{ core plugincore.Core }

func init() {
	plugins.Register(plugins.Meta{
		Order:       140,
		Key:         pluginKey,
		Name:        "IndexNow",
		Description: "将公开书籍、已发布章节与分类页的 URL 变更通知 IndexNow 参与搜索引擎；也可在页面被访问时实时推送、用触发地址立即推送积压或在后台手动推送。需要配置站点 HTTPS 地址。",
		Kind:        plugins.KindFeature,
		Builtin:     true,
		EnabledKey:  cfgEnabled,
		Models:      []any{&Config{}, &URL{}, &PushLog{}},
		Tables:      []string{"indexnow_logs", "indexnow_urls", "indexnow_config"},
	})
	plugincore.RegisterBehavior(&behavior{})
	plugincore.RegisterJob(jobSubmit, func(core plugincore.Core) func(context.Context, json.RawMessage) error {
		return (&behavior{core: core}).submitPending
	})
	plugincore.OnPublicURLsChanged(func(core plugincore.Core, urls []string) {
		if core.PluginEnabled(pluginKey) {
			(&behavior{core: core}).recordAndEnqueue(urls)
		}
	})
	plugincore.OnIndexableURLVisited(func(core plugincore.Core, kind, url string) {
		if core.PluginEnabled(pluginKey) {
			(&behavior{core: core}).onVisit(kind, url)
		}
	})
	plugincore.OnJobQueueSweep(func(core plugincore.Core, _ *jobqueue.Queue) {
		if core.PluginEnabled(pluginKey) {
			b := &behavior{core: core}
			b.purgeSubmitted(context.Background(), time.Now().UTC())
			b.purgeLogs()
			b.enqueueIfPending(context.Background())
		}
	})
}
