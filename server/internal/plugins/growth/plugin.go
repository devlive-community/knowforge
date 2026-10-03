package growth

import (
	"knowforge/server/internal/authz"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

func init() {
	plugincore.RegisterURLColumns("level_definitions", "icon_value") // 存储迁移：等级图标
	plugins.Register(plugins.Meta{
		Order:       60,
		Key:         plugins.KeyGrowth,
		Name:        "成长等级",
		Description: "用户成长等级：经验流水、等级、升级通知与公开徽标、每日签到与经验排行榜；经验来自阅读/创作/互动/签到/成就解锁等权威事件。默认关闭，启用后建表、注册权限并种子默认等级；禁用后页面/入口/接口一并停用，数据保留。",
		Kind:        plugins.KindFeature,
		Builtin:     true,
		EnabledKey:  "growth_enabled",
		Models:      []any{&models.LevelDefinition{}, &models.UserGrowthProfile{}, &models.ExperienceEvent{}, &models.UserLevelHistory{}, &models.ExperienceRule{}, &models.UserCheckin{}},
		Tables:      []string{"user_checkins", "user_level_histories", "experience_events", "user_growth_profiles", "level_definitions", "experience_rules"},
		AdminPerms:  []authz.Permission{authz.GrowthManage, authz.ExperienceAdjust},
		UserPerms:   []authz.Permission{authz.GrowthRead, authz.GrowthUpdate},
	})
}
