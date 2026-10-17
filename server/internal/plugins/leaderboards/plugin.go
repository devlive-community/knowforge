// Package leaderboards 排行榜插件：汇总各插件登记的榜单（plugincore.RegisterLeaderboard），统一排名、分页、
// 周期筛选与展示。自带创作榜、热门书籍、点赞榜与收藏榜；成长等级插件提供经验榜，用户关注插件提供粉丝榜，
// 书籍关注插件提供关注榜。管理员可开关每个榜单并调整顺序。
package leaderboards

import (
	"encoding/json"
	"strings"
	"time"

	"gorm.io/gorm"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

const pluginKey = plugins.KeyLeaderboards

// PermManage 开关榜单、调整顺序（管理员）。
const PermManage authz.Permission = "leaderboards:manage"

// cfgBoards 榜单设置（站点配置）：{"order":[...],"disabled":[...]}。
const cfgBoards = "leaderboards_config"

var allPeriods = []string{plugincore.LeaderboardAll, plugincore.LeaderboardMonth, plugincore.LeaderboardWeek}

func init() {
	plugins.Register(plugins.Meta{
		Order:       62,
		Key:         pluginKey,
		Name:        "排行榜",
		Description: "站点排行榜：创作榜、热门书籍、点赞榜、收藏榜，以及其他插件提供的经验榜（成长等级）、粉丝榜（用户关注）、关注榜（书籍关注），可按累计、近 30 天、近 7 天查看，登录用户可看到自己在用户榜中的名次。管理员可开关每个榜单并调整顺序。禁用后排行榜页面、入口与接口一并停用（默认启用）。",
		Kind:        plugins.KindFeature,
		Builtin:     true,
		AdminPerms:  []authz.Permission{PermManage},
	})
	plugincore.RegisterBehavior(&behavior{})
	plugincore.RegisterPublicSiteConfig(func(core plugincore.Core) map[string]any {
		keys := []string{}
		if core.PluginEnabled(pluginKey) {
			for _, l := range (&behavior{core: core}).boards() {
				keys = append(keys, l.Key)
			}
		}
		return map[string]any{"leaderboards": keys}
	})
	registerBuiltinBoards()
}

type behavior struct{ core plugincore.Core }

func (b *behavior) Key() string { return pluginKey }

type boardConfig struct {
	Order    []string `json:"order"`
	Disabled []string `json:"disabled"`
}

func (b *behavior) config() (boardConfig, bool) {
	var cfg boardConfig
	raw := strings.TrimSpace(b.core.GetSetting(cfgBoards))
	if raw == "" || json.Unmarshal([]byte(raw), &cfg) != nil {
		return boardConfig{}, false
	}
	return cfg, true
}

// boardState 一个榜单的当前状态（管理端展示用）。
type boardState struct {
	def           plugincore.Leaderboard
	pluginEnabled bool // 提供方插件已启用且满足开放条件
	enabled       bool // 管理员开关
}

// states 全部榜单，按管理员设置的顺序（未出现在设置中的按登记顺序排在后面）。
func (b *behavior) states() []boardState {
	cfg, saved := b.config()
	disabled := map[string]bool{}
	for _, k := range cfg.Disabled {
		disabled[k] = true
	}
	position := map[string]int{}
	for i, k := range cfg.Order {
		position[k] = i
	}
	defs := plugincore.Leaderboards()
	out := make([]boardState, 0, len(defs))
	for _, def := range defs {
		ok := def.Plugin == "" || b.core.PluginEnabled(def.Plugin)
		if ok && def.Available != nil {
			ok = def.Available(b.core)
		}
		enabled := !disabled[def.Key]
		if !saved && def.DefaultEnabled != nil {
			enabled = def.DefaultEnabled(b.core)
		}
		out = append(out, boardState{def: def, pluginEnabled: ok, enabled: enabled})
	}
	rank := func(s boardState) int {
		if p, found := position[s.def.Key]; found {
			return p
		}
		return len(cfg.Order) + s.def.Order
	}
	sortStable(out, func(x, y boardState) bool { return rank(x) < rank(y) })
	return out
}

// boards 当前对访客开放的榜单。
func (b *behavior) boards() []plugincore.Leaderboard {
	out := []plugincore.Leaderboard{}
	for _, s := range b.states() {
		if s.pluginEnabled && s.enabled {
			out = append(out, s.def)
		}
	}
	return out
}

func (b *behavior) board(key string) (plugincore.Leaderboard, bool) {
	for _, l := range b.boards() {
		if l.Key == key {
			return l, true
		}
	}
	return plugincore.Leaderboard{}, false
}

// —— 自带榜单：只依赖核心数据 ——

func registerBuiltinBoards() {
	plugincore.RegisterLeaderboard(plugincore.Leaderboard{
		Key: "creators", Kind: plugincore.LeaderboardUsers, Icon: "fa-pen-nib", Order: 20, Periods: allPeriods,
		// 公开书籍中已发布的章节数（按章节作者统计；周期按章节创建时间）
		Scores: func(core plugincore.Core, since *time.Time) *gorm.DB {
			q := core.Gorm().Model(&models.Document{}).
				Select("documents.user_id AS subject_id, COUNT(*) AS value").
				Joins("JOIN books ON books.id = documents.book_id AND books.deleted_at IS NULL AND books.is_public = ? AND books.status IN ?", true, core.PubliclyReadableBookStatuses()).
				Where("documents.status = ?", "published").Group("documents.user_id")
			if since != nil {
				q = q.Where("documents.created_at >= ?", *since)
			}
			return q
		},
	})
	plugincore.RegisterLeaderboard(plugincore.Leaderboard{
		Key: "hot-books", Kind: plugincore.LeaderboardBooks, Icon: "fa-fire", Order: 30, Periods: allPeriods,
		// 浏览量：累计取书籍浏览数，周期内按每日统计求和
		Scores: func(core plugincore.Core, since *time.Time) *gorm.DB {
			if since == nil {
				return core.Gorm().Model(&models.Book{}).Select("id AS subject_id, view_count AS value")
			}
			return core.Gorm().Model(&models.BookAnalyticsDaily{}).
				Select("book_id AS subject_id, SUM(view_count) AS value").
				Where("day >= ?", since.Format("2006-01-02")).Group("book_id")
		},
	})
	for i, kind := range []string{"like", "favorite"} {
		reaction := kind
		plugincore.RegisterLeaderboard(plugincore.Leaderboard{
			Key: map[string]string{"like": "liked-books", "favorite": "favorited-books"}[reaction], Kind: plugincore.LeaderboardBooks,
			Icon: map[string]string{"like": "fa-thumbs-up", "favorite": "fa-star"}[reaction], Order: 40 + i, Periods: allPeriods,
			Scores: func(core plugincore.Core, since *time.Time) *gorm.DB {
				q := core.Gorm().Model(&models.Reaction{}).Select("book_id AS subject_id, COUNT(*) AS value").
					Where("type = ?", reaction).Group("book_id")
				if since != nil {
					q = q.Where("created_at >= ?", *since)
				}
				return q
			},
		})
	}
}
