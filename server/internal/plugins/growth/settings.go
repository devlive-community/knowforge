package growth

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/plugins"
)

// 成长插件的站点级设置（站点配置键值）：经验榜上榜所需最少经验、签到。经验榜的开关在排行榜插件中；
// cfgLeaderboardEnabled 为旧版开关，只用于决定升级后经验榜的默认开放状态。
const (
	cfgLeaderboardEnabled = "growth_leaderboard_enabled"
	cfgLeaderboardMinXP   = "growth_leaderboard_min_xp"
	maxLeaderboardMinXP   = 1_000_000
	cfgCheckinEnabled     = "growth_checkin_enabled"
	cfgCheckinStreakDays  = "growth_checkin_streak_days"
	defaultStreakDays     = 7
	minStreakDays         = 2
	maxStreakDays         = 365
)

type growthSettings struct {
	LeaderboardMinXP int  `json:"leaderboard_min_xp"`
	CheckinEnabled   bool `json:"checkin_enabled"`
	// CheckinStreakDays 连续签到每满 N 天发一次「连续签到奖励」
	CheckinStreakDays int `json:"checkin_streak_days"`
}

// settings 读取成长设置：签到默认开放；最少上榜经验默认 1（有经验即可上榜）；连续签到奖励默认每 7 天。
func (b *behavior) settings() growthSettings {
	s := growthSettings{
		LeaderboardMinXP: 1,
		CheckinEnabled:   b.core.GetSetting(cfgCheckinEnabled) != "false", CheckinStreakDays: defaultStreakDays,
	}
	if v := b.core.AtoiDefault(b.core.GetSetting(cfgLeaderboardMinXP), 1); v > 1 {
		s.LeaderboardMinXP = v
	}
	if v := b.core.AtoiDefault(b.core.GetSetting(cfgCheckinStreakDays), defaultStreakDays); v >= minStreakDays && v <= maxStreakDays {
		s.CheckinStreakDays = v
	}
	return s
}

// AdminGetGrowthSettings GET /admin/growth/settings
func (b *behavior) AdminGetGrowthSettings(c *gin.Context) { b.core.OK(c, b.settings()) }

// AdminUpdateGrowthSettings PUT /admin/growth/settings {leaderboard_min_xp, checkin_enabled, checkin_streak_days}
func (b *behavior) AdminUpdateGrowthSettings(c *gin.Context) {
	core := b.core
	var req growthSettings
	req = b.settings() // 未传的字段保持原值
	if err := c.ShouldBindJSON(&req); err != nil || req.LeaderboardMinXP < 1 || req.LeaderboardMinXP > maxLeaderboardMinXP {
		core.Fail(c, http.StatusBadRequest, "参数错误：最少上榜经验需为 1 到 1000000 之间的整数")
		return
	}
	if req.CheckinStreakDays < minStreakDays || req.CheckinStreakDays > maxStreakDays {
		core.Fail(c, http.StatusBadRequest, "参数错误：连续签到奖励周期需为 2 到 365 天")
		return
	}
	if err := core.SetSetting(cfgLeaderboardMinXP, strconv.Itoa(req.LeaderboardMinXP), "成长：上榜所需最少经验"); err != nil {
		core.Fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	for key, value := range map[string]string{
		cfgCheckinEnabled:    strconv.FormatBool(req.CheckinEnabled),
		cfgCheckinStreakDays: strconv.Itoa(req.CheckinStreakDays),
	} {
		if err := core.SetSetting(key, value, "成长：签到设置"); err != nil {
			core.Fail(c, http.StatusInternalServerError, "保存失败")
			return
		}
	}
	core.RecordAudit(c, "growth.settings_updated", "growth", "settings", "成长设置", changedFields("leaderboard_min_xp", "checkin_enabled", "checkin_streak_days"))
	core.OK(c, b.settings())
}

// publicSettings 公开设置（/growth/settings）。
func (b *behavior) publicSettings() gin.H {
	enabled := b.core.PluginEnabled(plugins.KeyGrowth)
	s := b.settings()
	return gin.H{"enabled": enabled, "checkin_enabled": enabled && s.CheckinEnabled}
}
