package growth

import (
	"time"

	"gorm.io/gorm"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// 经验榜：登记到排行榜插件。累计 = 累计经验；近 7/30 天 = 周期内新增经验（按流水求和，扣回的负经验同样计入）。
// 只统计公开成长资料且经验达到「最少上榜经验」的用户；隐藏资料的用户仍可看到自己的名次。
func init() {
	plugincore.RegisterLeaderboard(plugincore.Leaderboard{
		Key: "xp", Plugin: plugins.KeyGrowth, Kind: plugincore.LeaderboardUsers, Icon: "fa-ranking-star", Order: 10,
		Periods: []string{plugincore.LeaderboardAll, plugincore.LeaderboardMonth, plugincore.LeaderboardWeek},
		Scores: func(core plugincore.Core, since *time.Time) *gorm.DB {
			b := &behavior{core: core}
			return b.xpScores(since, b.settings().LeaderboardMinXP)
		},
		MyScore: func(core plugincore.Core, userID uint, since *time.Time) (int64, bool, map[string]any) {
			b := &behavior{core: core}
			p := b.growthProfile(userID)
			xp := p.LifetimeXP
			if since != nil {
				core.Gorm().Model(&models.ExperienceEvent{}).Where("user_id = ? AND created_at >= ?", userID, *since).
					Select("COALESCE(SUM(final_xp),0)").Scan(&xp)
			}
			return xp, xp >= int64(b.settings().LeaderboardMinXP), map[string]any{"public": p.Public, "level": b.levelsByNumber()[p.CurrentLevel]}
		},
		Extras: func(core plugincore.Core, ids []uint) map[uint]any {
			b := &behavior{core: core}
			levels := b.levelsByNumber()
			var profiles []models.UserGrowthProfile
			core.Gorm().Where("user_id IN ?", ids).Find(&profiles)
			out := make(map[uint]any, len(profiles))
			for _, p := range profiles {
				if lv := levels[p.CurrentLevel]; lv != nil {
					out[p.UserID] = map[string]any{"level": lv}
				}
			}
			return out
		},
		// 升级前关闭了旧的「经验排行榜」开关的站点，经验榜默认保持关闭
		DefaultEnabled: func(core plugincore.Core) bool { return core.GetSetting(cfgLeaderboardEnabled) != "false" },
	})
}

// xpScores 公开成长资料的用户在周期内的经验（≥ minXP）：列 subject_id、value。
func (b *behavior) xpScores(since *time.Time, minXP int) *gorm.DB {
	db := b.core.Gorm()
	if since != nil {
		sums := db.Model(&models.ExperienceEvent{}).
			Select("user_id, SUM(final_xp) AS xp").
			Where("created_at >= ?", *since).
			Group("user_id")
		return db.Table("user_growth_profiles AS p").
			Joins("JOIN (?) s ON s.user_id = p.user_id", sums).
			Where("p.public = ? AND s.xp >= ?", true, minXP).
			Select("p.user_id AS subject_id, s.xp AS value")
	}
	return db.Table("user_growth_profiles AS p").
		Where("p.public = ? AND p.lifetime_xp >= ?", true, minXP).
		Select("p.user_id AS subject_id, p.lifetime_xp AS value")
}

func (b *behavior) levelsByNumber() map[int]*models.LevelDefinition {
	var defs []models.LevelDefinition
	b.core.Gorm().Find(&defs)
	out := make(map[int]*models.LevelDefinition, len(defs))
	for i := range defs {
		out[defs[i].Level] = &defs[i]
	}
	return out
}
