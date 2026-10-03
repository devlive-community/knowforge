// Package achievements 成就插件：成就定义/规则/指标、事件驱动的评估与授予、用户成就页与公开展示、
// 管理端（定义、图标、授予/撤销、重算）及成就多语言。通过 plugincore.Core 访问核心能力，不依赖 app 包；
// 数据模型仍在 models 包（models.SeedI18n 的历史语言迁移依赖成就定义表）。
package achievements

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/i18ntext"
	"knowforge/server/internal/jobqueue"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// behavior 插件行为；core 在 RegisterRoutes 时注入（任务/钩子回调各自携带 core）。
type behavior struct{ core plugincore.Core }

func init() {
	plugincore.RegisterURLColumns("achievement_definitions", "icon_value") // 存储迁移：成就图标（上传的图片/SVG 地址）
	plugincore.RegisterURLColumns("achievement_assets", "url")
	plugincore.RegisterBehavior(&behavior{})
	i18ntext.Register("notify.achievement.unlocked", map[string]string{"zh-CN": "已解锁成就「{name}」", "en": "Achievement unlocked: {name}"})
	i18ntext.Register("notify.achievement.granted", map[string]string{"zh-CN": "已获得成就「{name}」", "en": "You have been awarded the achievement: {name}"})
	// 业务活动 → 幂等的成就评估事件
	plugincore.OnActivity(func(core plugincore.Core, ev plugincore.ActivityEvent) {
		(&behavior{core: core}).recordAchievementEvent(ev.UserID, ev.Type, ev.SourceType, ev.SourceID, ev.DedupeKey)
	})
	plugincore.RegisterJob(achievementRecalculateJobType, func(core plugincore.Core) func(context.Context, json.RawMessage) error {
		return (&behavior{core: core}).runAchievementRecalculateJob
	})
	plugincore.RegisterJob(achievementEvaluateJobType, func(core plugincore.Core) func(context.Context, json.RawMessage) error {
		return (&behavior{core: core}).runAchievementEvaluateJob
	})
	// 队列创建与周期巡检时补投未处理的事件
	plugincore.OnJobQueueSweep(func(core plugincore.Core, queue *jobqueue.Queue) {
		(&behavior{core: core}).enqueuePendingAchievementEvents(queue)
	})
	// 启用后全量重算（避免还要去模块设置里再保存一次）
	plugincore.OnPluginEnabled(plugins.KeyAchievements, func(core plugincore.Core) error {
		_, err := (&behavior{core: core}).enqueueAchievementRecalculation(0)
		return err
	})
	// 公开站点配置：achievements_enabled（前端据此显示成就入口；未设置时不下发，与迁移前一致）
	plugincore.RegisterPublicSiteConfig(func(core plugincore.Core) map[string]any {
		if v := core.GetSetting(cfgAchievementsEnabled); v != "" {
			return map[string]any{cfgAchievementsEnabled: v}
		}
		return nil
	})
	// 删除用户时一并清理其成就数据
	plugincore.RegisterUserDataModels(&models.UserAchievementProgress{}, &models.UserAchievement{}, &models.AchievementEvent{})
	plugins.Register(plugins.Meta{
		Order:       20,
		Key:         plugins.KeyAchievements,
		Name:        "成就系统",
		Description: "为用户提供成就、徽章与进度追踪。启用后管理后台显示「成就管理」，用户端显示成就页；禁用后相关页面与接口一并停用。首次启用时建表、注册权限，卸载可清除数据。",
		Kind:        plugins.KindFeature,
		Builtin:     true,
		EnabledKey:  "achievements_enabled",
		Models: []any{
			&models.AchievementAsset{}, &models.AchievementDefinition{}, &models.AchievementRule{},
			&models.AchievementDefinitionVersion{}, &models.UserAchievementProgress{},
			&models.UserAchievement{}, &models.AchievementEvent{},
		},
		Tables: []string{
			"achievement_events", "user_achievements", "user_achievement_progresses",
			"achievement_definition_versions", "achievement_rules", "achievement_definitions", "achievement_assets",
		},
		AdminPerms: []authz.Permission{authz.AchievementManage, authz.AchievementGrant},
		UserPerms:  []authz.Permission{authz.AchievementRead, authz.AchievementUpdate},
	})
}

func (am *behavior) Key() string { return plugins.KeyAchievements }

// RegisterRoutes 挂载成就路由（路径与中间件与迁移前一致）。
func (am *behavior) RegisterRoutes(api *gin.RouterGroup, core plugincore.Core) {
	am.core = core
	feature := core.RequireFeaturePlugin(plugins.KeyAchievements)
	manage := core.RequirePermission(authz.AchievementManage)
	grant := core.RequirePermission(authz.AchievementGrant)

	// 公开
	api.GET("/users/:username/achievements", core.OptionalAuth(), feature, am.PublicUserAchievements)
	api.GET("/achievements/settings", core.OptionalAuth(), am.PublicAchievementSettings)

	// 我的成就
	mine := api.Group("/users/me/achievements", core.RequireAuth(), feature)
	mine.GET("", core.RequirePermission(authz.AchievementRead), am.MyAchievements)
	mine.PUT("/:id/display", core.RequirePermission(authz.AchievementUpdate), am.UpdateMyAchievementDisplay)

	// 管理端。achievement-settings 不挂启用守卫：禁用后仍可读取（返回 enabled=false），启用/禁用统一走插件页。
	admin := api.Group("", core.RequireAuth(), core.RequireAdmin())
	admin.GET("/admin/achievement-settings", manage, am.AdminGetAchievementSettings)
	admin.PUT("/admin/achievement-settings", manage, am.AdminUpdateAchievementSettings)
	admin.GET("/admin/i18n/resources/achievement/:id", manage, am.AdminResourceTranslations)
	admin.PUT("/admin/i18n/resources/achievement/:id", manage, am.AdminResourceTranslations)
	ach := admin.Group("", feature)
	ach.GET("/admin/achievement-metrics", manage, am.AdminAchievementMetrics)
	ach.GET("/admin/achievements", manage, am.AdminListAchievements)
	ach.POST("/admin/achievements", manage, am.AdminCreateAchievement)
	ach.GET("/admin/achievements/:id", manage, am.AdminGetAchievement)
	ach.PUT("/admin/achievements/:id", manage, am.AdminUpdateAchievement)
	ach.DELETE("/admin/achievements/:id", manage, am.AdminDeleteAchievement)
	ach.POST("/admin/achievements/:id/recalculate", manage, am.AdminRecalculateAchievement)
	ach.POST("/admin/achievement-icons", manage, am.AdminUploadAchievementIcon)
	ach.GET("/admin/achievement-presets", manage, am.AdminListPresets)
	ach.POST("/admin/achievement-presets/install", manage, am.AdminInstallPresets)
	ach.GET("/admin/achievement-grants", grant, am.AdminListAchievementGrants)
	ach.POST("/admin/achievement-grants", grant, am.AdminGrantAchievement)
	ach.POST("/admin/achievement-grants/:id/revoke", grant, am.AdminRevokeAchievement)
}

func currentTime() time.Time { return time.Now() }

func auditID(id uint) string { return strconv.FormatUint(uint64(id), 10) }

func changedFields(fields ...string) map[string]any { return map[string]any{"changed_fields": fields} }

// truncateText 按字符（rune）截断。
func truncateText(value string, maxRunes int) string {
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes])
}

// dayStart 本地时区当天零点（与核心书籍分析的日桶一致）。
func dayStart(value time.Time) time.Time {
	local := value.In(time.Local)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.Local)
}

// weekStart 本地时区所在周的周一零点（与核心读者留存的周桶一致）。
func weekStart(t time.Time) time.Time {
	day := dayStart(t)
	offset := (int(day.Weekday()) + 6) % 7 // 周一=0 … 周日=6
	return day.AddDate(0, 0, -offset)
}
