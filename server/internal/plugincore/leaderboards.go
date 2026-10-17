package plugincore

import (
	"sort"
	"time"

	"gorm.io/gorm"
)

// —— 排行榜：插件登记榜单（统计口径），由「排行榜」插件统一排名、分页、过滤与展示。——

// 榜单对象类型。
const (
	LeaderboardUsers = "user" // 用户榜（只列启用中的用户）
	LeaderboardBooks = "book" // 书籍榜（只列公开可读的书）
)

// 统计周期：累计、近 30 天、近 7 天。
const (
	LeaderboardAll   = "all"
	LeaderboardMonth = "month"
	LeaderboardWeek  = "week"
)

// Leaderboard 一个榜单。Scores 返回周期内各对象得分的子查询，列为 subject_id（用户或书籍 ID）与 value；
// since 为 nil 表示累计。对象是否公开由排行榜插件统一过滤，Scores 只需处理本榜特有的条件（如公开成长资料）。
type Leaderboard struct {
	Key     string
	Plugin  string // 提供该榜单的插件 key（插件禁用时榜单不显示）；空表示排行榜插件自带
	Kind    string // LeaderboardUsers | LeaderboardBooks
	Icon    string // FontAwesome 图标名
	Order   int
	Periods []string
	Scores  func(core Core, since *time.Time) *gorm.DB
	// MyScore 可选：用户榜中本人的得分与是否参与排名（如隐藏成长资料的用户仍可看到自己的名次），extra 合并进返回的 me。
	MyScore func(core Core, userID uint, since *time.Time) (value int64, ranked bool, extra map[string]any)
	// Extras 可选：榜单项的附加信息（如等级），按对象 ID 返回。
	Extras func(core Core, ids []uint) map[uint]any
	// Available 可选：除插件启用外的开放条件。
	Available func(core Core) bool
	// DefaultEnabled 可选：管理员尚未设置时该榜单是否开放（缺省开放）。
	DefaultEnabled func(core Core) bool
}

var leaderboards []Leaderboard

// RegisterLeaderboard 供插件在 init() 中登记榜单。
func RegisterLeaderboard(l Leaderboard) { leaderboards = append(leaderboards, l) }

// Leaderboards 已登记的全部榜单（按 Order 排序）。
func Leaderboards() []Leaderboard {
	out := append([]Leaderboard(nil), leaderboards...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

// LeaderboardSince 周期的起始时间；累计返回 nil。
func LeaderboardSince(period string, now time.Time) *time.Time {
	var d time.Duration
	switch period {
	case LeaderboardWeek:
		d = 7 * 24 * time.Hour
	case LeaderboardMonth:
		d = 30 * 24 * time.Hour
	default:
		return nil
	}
	t := now.Add(-d)
	return &t
}
