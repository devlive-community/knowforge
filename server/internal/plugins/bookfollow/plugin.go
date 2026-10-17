package bookfollow

import (
	"time"

	"gorm.io/gorm"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/i18ntext"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

func init() {
	// 章节更新通知的多语言模板（与前端字典同键）
	i18ntext.Register("notify.follow.chapterPublished", map[string]string{"zh-CN": "《{book}》更新了新章节：{chapter}", "en": `"{book}" has a new chapter: {chapter}`})
	plugins.Register(plugins.Meta{
		Order:       50,
		Key:         plugins.KeyBookFollow,
		Name:        "书籍关注",
		Description: "用户可关注书籍，作品更新（新章节/状态）时收到通知；用户可在通知偏好中开关。禁用后关注入口、我的关注、相关接口与更新通知一并停用（默认启用）。首次启用建表、注册权限，卸载可清除数据。",
		Kind:        plugins.KindFeature,
		Builtin:     true,
		Models:      []any{&BookFollow{}},
		Tables:      []string{"book_follows"},
		UserPerms:   []authz.Permission{authz.FollowRead, authz.FollowCreate, authz.FollowDelete},
	})
	// 关注榜（排行榜插件）：累计为关注人数，周期内为新增关注人数
	plugincore.RegisterLeaderboard(plugincore.Leaderboard{
		Key: "followed-books", Plugin: plugins.KeyBookFollow, Kind: plugincore.LeaderboardBooks, Icon: "fa-bell", Order: 50,
		Periods: []string{plugincore.LeaderboardAll, plugincore.LeaderboardMonth, plugincore.LeaderboardWeek},
		Scores: func(core plugincore.Core, since *time.Time) *gorm.DB {
			q := core.Gorm().Model(&BookFollow{}).Select("book_id AS subject_id, COUNT(*) AS value").Group("book_id")
			if since != nil {
				q = q.Where("created_at >= ?", *since)
			}
			return q
		},
	})
}
