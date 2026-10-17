// Package userfollow 用户关注插件：用户可关注作者，个人主页显示关注者与关注数并可查看列表，
// 「我的关注」可看关注作者最近更新的公开书籍；有人关注自己、关注的作者更新公开书籍时收到通知
// （通知类型 social，可在通知偏好中关闭）。与「书籍关注」互不依赖：已关注该书的读者不重复收到作者更新通知。
package userfollow

import (
	"time"

	"gorm.io/gorm"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/i18ntext"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

const (
	pluginKey     = plugins.KeyUserFollow
	bookFollowKey = plugins.KeyBookFollow
)

// 关注 / 取消关注作者（登录用户）；关注列表与关注数公开，无需权限。
const (
	PermCreate authz.Permission = "user-follow:create"
	PermDelete authz.Permission = "user-follow:delete"
)

// notifyType 通知类型，对应通知偏好 social。
const notifyType = "social"

// UserFollow 关注关系：UserID 关注 FolloweeID。UserID 为关注者（列名 user_id，删除用户时由核心一并清理），
// 被关注者删除后其关系因关联不到用户而不再出现在列表与计数中。
type UserFollow struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	UserID     uint      `gorm:"uniqueIndex:uk_user_follow;not null" json:"user_id"`
	FolloweeID uint      `gorm:"uniqueIndex:uk_user_follow;index;not null" json:"followee_id"`
	CreatedAt  time.Time `gorm:"index" json:"created_at"`
}

func init() {
	i18ntext.Register("notify.userFollow.followed", map[string]string{"zh-CN": "{user} 关注了你", "en": "{user} followed you"})
	i18ntext.Register("notify.userFollow.chapterPublished", map[string]string{"zh-CN": "你关注的 {user} 更新了《{book}》：{chapter}", "en": `{user}, whom you follow, updated "{book}": {chapter}`})
	plugins.Register(plugins.Meta{
		Order:       55,
		Key:         pluginKey,
		Name:        "用户关注",
		Description: "用户可关注作者：个人主页显示关注者与关注数并可查看列表，「我的关注」可看关注作者最近更新的公开书籍；有人关注自己、关注的作者更新公开书籍时收到通知（可在通知偏好中关闭）。禁用后关注入口、列表、接口与通知一并停用（默认启用），数据保留；卸载可清除数据。",
		Kind:        plugins.KindFeature,
		Builtin:     true,
		Models:      []any{&UserFollow{}},
		Tables:      []string{"user_follows"},
		UserPerms:   []authz.Permission{PermCreate, PermDelete},
	})
	plugincore.RegisterBehavior(&behavior{})
	plugincore.RegisterUserDataModels(&UserFollow{})
	// 粉丝榜（排行榜插件）：累计为关注者数，周期内为新增关注者数；只计启用中的关注者
	plugincore.RegisterLeaderboard(plugincore.Leaderboard{
		Key: "followers", Plugin: pluginKey, Kind: plugincore.LeaderboardUsers, Icon: "fa-user-group", Order: 15,
		Periods: []string{plugincore.LeaderboardAll, plugincore.LeaderboardMonth, plugincore.LeaderboardWeek},
		Scores: func(core plugincore.Core, since *time.Time) *gorm.DB {
			q := core.Gorm().Model(&UserFollow{}).Select("user_follows.followee_id AS subject_id, COUNT(*) AS value").
				Joins("JOIN users f ON f.id = user_follows.user_id AND f.is_active = ?", true).Group("user_follows.followee_id")
			if since != nil {
				q = q.Where("user_follows.created_at >= ?", *since)
			}
			return q
		},
	})
}
