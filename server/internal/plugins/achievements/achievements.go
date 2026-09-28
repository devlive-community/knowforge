package achievements

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	cfgAchievementsEnabled       = "achievements_enabled"
	cfgAchievementsPublic        = "achievements_public_profile_enabled"
	cfgAchievementsNotifications = "achievements_notifications_enabled"
	cfgAchievementsAllowHide     = "achievements_allow_user_hide"
	cfgAchievementsShowcaseLimit = "achievements_showcase_limit"
)

// maxRewardXP 单个成就的奖励经验上限。
const maxRewardXP = 100000

var achievementKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,79}$`)

type achievementSettings struct {
	Enabled              bool `json:"enabled"`
	PublicProfileEnabled bool `json:"public_profile_enabled"`
	NotificationsEnabled bool `json:"notifications_enabled"`
	AllowUserHide        bool `json:"allow_user_hide"`
	ShowcaseLimit        int  `json:"showcase_limit"`
}

func achievementBoolValue(raw string, fallback bool) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	return raw == "true"
}

func (am *behavior) achievementSettings() achievementSettings {
	rows := []models.SiteConfig{}
	am.core.Gorm().Where("config_key IN ?", []string{cfgAchievementsEnabled, cfgAchievementsPublic, cfgAchievementsNotifications, cfgAchievementsAllowHide, cfgAchievementsShowcaseLimit}).Find(&rows)
	values := map[string]string{}
	for _, row := range rows {
		values[row.ConfigKey] = row.ConfigValue
	}
	limit := am.core.AtoiDefault(values[cfgAchievementsShowcaseLimit], 6)
	if limit < 1 {
		limit = 1
	} else if limit > 12 {
		limit = 12
	}
	return achievementSettings{
		Enabled:              achievementBoolValue(values[cfgAchievementsEnabled], false),
		PublicProfileEnabled: achievementBoolValue(values[cfgAchievementsPublic], true),
		NotificationsEnabled: achievementBoolValue(values[cfgAchievementsNotifications], true),
		AllowUserHide:        achievementBoolValue(values[cfgAchievementsAllowHide], true),
		ShowcaseLimit:        limit,
	}
}

// PublicAchievementSettings GET /achievements/settings 返回不敏感的模块状态。
func (am *behavior) PublicAchievementSettings(c *gin.Context) {
	s := am.achievementSettings()
	am.core.OK(c, gin.H{"enabled": s.Enabled, "public_profile_enabled": s.PublicProfileEnabled, "showcase_limit": s.ShowcaseLimit})
}

func (am *behavior) AdminGetAchievementSettings(c *gin.Context) {
	am.core.OK(c, am.achievementSettings())
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// AdminUpdateAchievementSettings PUT /admin/achievement-settings。
func (am *behavior) AdminUpdateAchievementSettings(c *gin.Context) {
	var req achievementSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		am.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if req.ShowcaseLimit < 1 {
		req.ShowcaseLimit = 1
	} else if req.ShowcaseLimit > 12 {
		req.ShowcaseLimit = 12
	}
	// 注意：成就总开关（cfgAchievementsEnabled）由「成就」插件启用状态唯一掌管，此处不再写入，
	// 避免与插件开关冲突、以及「启用插件后还要来这里保存一次」的问题。
	settings := []struct{ key, value, description string }{
		{cfgAchievementsPublic, boolText(req.PublicProfileEnabled), "公开主页展示成就"},
		{cfgAchievementsNotifications, boolText(req.NotificationsEnabled), "成就解锁站内通知"},
		{cfgAchievementsAllowHide, boolText(req.AllowUserHide), "允许用户隐藏成就"},
		{cfgAchievementsShowcaseLimit, strconv.Itoa(req.ShowcaseLimit), "公开主页成就陈列数量"},
	}
	for _, item := range settings {
		if err := am.core.SetSetting(item.key, item.value, item.description); err != nil {
			am.core.Fail(c, http.StatusInternalServerError, "保存成就设置失败")
			return
		}
	}
	am.core.RecordAudit(c, "achievement.settings_updated", "achievement", "settings", "成就模块设置", changedFields(
		"public_profile_enabled", "notifications_enabled", "allow_user_hide", "showcase_limit",
	))
	am.core.OK(c, am.achievementSettings())
}

type achievementMetric struct {
	Key            string   `json:"key"`
	Label          string   `json:"label"`
	Category       string   `json:"category"`
	Description    string   `json:"description"`
	Aggregation    string   `json:"aggregation"`
	Unit           string   `json:"unit"`
	Windows        []string `json:"windows"`
	AllowedFilters []string `json:"allowed_filters"`
}

var achievementMetrics = []achievementMetric{
	{Key: "reading.chapters_read", Label: "首次读过章节", Category: "reading", Description: "按章节去重的首次阅读数量", Aggregation: "distinct_count", Unit: "章", Windows: []string{"lifetime", "calendar_day", "calendar_week", "calendar_month", "rolling_days"}},
	{Key: "reading.books_started", Label: "开始阅读书籍", Category: "reading", Description: "产生阅读进度的不同书籍数", Aggregation: "distinct_count", Unit: "本", Windows: []string{"lifetime", "rolling_days"}},
	{Key: "reading.minutes", Label: "累计阅读时长", Category: "reading", Description: "阅读器上报的有效阅读分钟数", Aggregation: "sum", Unit: "分钟", Windows: []string{"lifetime", "calendar_day", "calendar_week", "calendar_month", "rolling_days"}},
	{Key: "reading.annotations", Label: "创建阅读标注", Category: "reading", Description: "私人划线、笔记或章节书签数量", Aggregation: "count", Unit: "条", Windows: []string{"lifetime", "rolling_days"}, AllowedFilters: []string{"kind"}},
	{Key: "reading.reading_days", Label: "阅读活跃天数", Category: "reading", Description: "有有效阅读时长的自然日去重天数", Aggregation: "distinct_count", Unit: "天", Windows: []string{"lifetime", "rolling_days"}},
	{Key: "social.likes_given", Label: "点赞书籍", Category: "community", Description: "当前仍保留的书籍点赞数", Aggregation: "count", Unit: "次", Windows: []string{"lifetime", "rolling_days"}},
	{Key: "social.favorites_given", Label: "收藏书籍", Category: "community", Description: "当前仍保留的书籍收藏数", Aggregation: "count", Unit: "次", Windows: []string{"lifetime", "rolling_days"}},
	{Key: "social.comments_created", Label: "发表评论", Category: "community", Description: "已发布评论或回复数量", Aggregation: "count", Unit: "条", Windows: []string{"lifetime", "rolling_days"}, AllowedFilters: []string{"replies_only"}},
	{Key: "creator.books_created", Label: "创建书籍", Category: "creation", Description: "本人当前拥有的书籍数量", Aggregation: "count", Unit: "本", Windows: []string{"lifetime", "rolling_days"}, AllowedFilters: []string{"status", "is_public"}},
	{Key: "creator.documents_created", Label: "创建章节", Category: "creation", Description: "本人创建且未删除的章节数量", Aggregation: "count", Unit: "章", Windows: []string{"lifetime", "rolling_days"}, AllowedFilters: []string{"status"}},
	{Key: "creator.views_received", Label: "作品获得浏览", Category: "creation", Description: "本人书籍累计页面浏览量（PV）", Aggregation: "sum", Unit: "次", Windows: []string{"lifetime"}, AllowedFilters: []string{"status", "is_public"}},
	{Key: "creator.likes_received", Label: "作品获得点赞", Category: "creation", Description: "本人书籍当前点赞总数", Aggregation: "count", Unit: "次", Windows: []string{"lifetime", "rolling_days"}},
	{Key: "creator.favorites_received", Label: "作品获得收藏", Category: "creation", Description: "本人书籍当前收藏总数", Aggregation: "count", Unit: "次", Windows: []string{"lifetime", "rolling_days"}},
	{Key: "creator.comments_received", Label: "作品获得评论", Category: "creation", Description: "本人作品收到的已发布评论数", Aggregation: "count", Unit: "条", Windows: []string{"lifetime", "rolling_days"}, AllowedFilters: []string{"replies_only"}},
	{Key: "creator.published_books", Label: "发布书籍", Category: "creation", Description: "本人已发布状态的书籍数量", Aggregation: "count", Unit: "本", Windows: []string{"lifetime", "rolling_days"}},
	{Key: "creator.tags_used", Label: "使用过的标签", Category: "creation", Description: "本人书籍上使用过的不同标签数", Aggregation: "distinct_count", Unit: "个", Windows: []string{"lifetime"}},
	{Key: "creator.published_words", Label: "累计发布字数", Category: "creation", Description: "本人已发布章节正文的累计字符数", Aggregation: "sum", Unit: "字", Windows: []string{"lifetime"}},
	{Key: "reading.books_completed", Label: "读完书籍", Category: "reading", Description: "已读完（读过全部已发布章节）的书籍数", Aggregation: "distinct_count", Unit: "本", Windows: []string{"lifetime"}},
	{Key: "growth.total_xp", Label: "累计成长经验", Category: "account", Description: "成长系统累计经验（需启用成长插件）", Aggregation: "current", Unit: "经验", Windows: []string{"lifetime"}},
	{Key: "growth.current_level", Label: "达到成长等级", Category: "account", Description: "当前成长等级编号（需启用成长插件）", Aggregation: "current", Unit: "级", Windows: []string{"lifetime"}},
	{Key: "account.age_days", Label: "账号创建天数", Category: "account", Description: "从注册日期到当前的自然天数", Aggregation: "current", Unit: "天", Windows: []string{"lifetime"}},
	{Key: "account.invited_users", Label: "邀请注册用户", Category: "account", Description: "通过本人邀请码注册的用户数", Aggregation: "count", Unit: "人", Windows: []string{"lifetime", "rolling_days"}},
	{Key: "account.oauth_bindings", Label: "绑定第三方账号", Category: "account", Description: "当前绑定的 OAuth 提供方数量", Aggregation: "count", Unit: "个", Windows: []string{"lifetime"}},
	{Key: "account.email_verified", Label: "完成邮箱验证", Category: "account", Description: "邮箱已经通过验证", Aggregation: "current", Unit: "", Windows: []string{"lifetime"}},
	{Key: "account.two_factor_enabled", Label: "开启二次认证", Category: "account", Description: "账号当前已经开启 TOTP 二次认证", Aggregation: "current", Unit: "", Windows: []string{"lifetime"}},
	{Key: "checkin.total_days", Label: "累计签到天数", Category: "account", Description: "每日签到的累计天数（需启用成长插件）", Aggregation: "count", Unit: "天", Windows: []string{"lifetime", "calendar_month", "rolling_days"}},
	{Key: "checkin.current_streak", Label: "当前连续签到", Category: "account", Description: "截至今天（或昨天）的连续签到天数，中断即归零（需启用成长插件）", Aggregation: "current", Unit: "天", Windows: []string{"lifetime"}},
	{Key: "checkin.longest_streak", Label: "最长连续签到", Category: "account", Description: "历史上最长的一次连续签到天数（需启用成长插件）", Aggregation: "current", Unit: "天", Windows: []string{"lifetime"}},
}

func metricByKey(key string) (achievementMetric, bool) {
	for _, metric := range achievementMetrics {
		if metric.Key == key {
			return metric, true
		}
	}
	if m, ok := plugincore.UserMetricByKey(key); ok {
		return fromUserMetric(m), true
	}
	return achievementMetric{}, false
}

// fromUserMetric 其他插件经 plugincore 登记的用户指标（如会员累计天数）。
func fromUserMetric(m plugincore.UserMetric) achievementMetric {
	return achievementMetric{Key: m.Key, Label: m.Label, Category: m.Category, Description: m.Description,
		Aggregation: m.Aggregation, Unit: m.Unit, Windows: m.Windows, AllowedFilters: []string{}}
}

// metricAvailable 插件登记的指标需其所属插件可用（内置指标恒为可用）。
func (am *behavior) metricAvailable(key string) bool {
	if m, ok := plugincore.UserMetricByKey(key); ok {
		return m.Available == nil || m.Available(am.core)
	}
	return true
}

func (am *behavior) AdminAchievementMetrics(c *gin.Context) {
	// 成长（经验/等级）指标依赖「成长」插件：插件禁用时不在规则构建器里暴露，
	// 避免管理员配出永远为 0 的规则（评估侧也已对 growth.* 做同样门禁）。
	growthOn := am.core.PluginEnabled(plugins.KeyGrowth)
	items := make([]achievementMetric, 0, len(achievementMetrics))
	for _, metric := range achievementMetrics {
		if !growthOn && strings.HasPrefix(metric.Key, "growth.") {
			continue
		}
		if metric.AllowedFilters == nil {
			metric.AllowedFilters = []string{}
		}
		items = append(items, metric)
	}
	// 其他插件登记的指标：所属插件可用时才出现
	for _, m := range plugincore.UserMetrics() {
		if m.Available == nil || m.Available(am.core) {
			items = append(items, fromUserMetric(m))
		}
	}
	am.core.OK(c, gin.H{"items": items})
}

type achievementRuleRequest struct {
	MetricKey   string         `json:"metric_key"`
	Operator    string         `json:"operator"`
	TargetValue int64          `json:"target_value"`
	TargetMax   int64          `json:"target_max"`
	WindowType  string         `json:"window_type"`
	WindowValue int            `json:"window_value"`
	DistinctBy  string         `json:"distinct_by"`
	Filters     map[string]any `json:"filters"`
}

type achievementDefinitionRequest struct {
	Translations       map[string]plugincore.ResourceTranslation `json:"translations"`
	Key                string                                    `json:"key"`
	Name               string                                    `json:"name"`
	NameEn             string                                    `json:"name_en"`
	Description        string                                    `json:"description"`
	DescriptionEn      string                                    `json:"description_en"`
	LockedHint         string                                    `json:"locked_hint"`
	LockedHintEn       string                                    `json:"locked_hint_en"`
	Category           string                                    `json:"category"`
	Status             string                                    `json:"status"`
	Rarity             string                                    `json:"rarity"`
	IconType           string                                    `json:"icon_type"`
	IconValue          string                                    `json:"icon_value"`
	AssetID            *uint                                     `json:"asset_id"`
	SeriesKey          string                                    `json:"series_key"`
	Tier               int                                       `json:"tier"`
	RewardXP           int                                       `json:"reward_xp"`
	SupersedesPrevious bool                                      `json:"supersedes_previous"`
	RuleLogic          string                                    `json:"rule_logic"`
	GrantMode          string                                    `json:"grant_mode"`
	Visibility         string                                    `json:"visibility"`
	ProgressMode       string                                    `json:"progress_mode"`
	ActiveFrom         *time.Time                                `json:"active_from"`
	ActiveUntil        *time.Time                                `json:"active_until"`
	SortOrder          int                                       `json:"sort_order"`
	Rules              []achievementRuleRequest                  `json:"rules"`
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func normalizeAchievementRequest(req *achievementDefinitionRequest) error {
	req.Key = strings.ToLower(strings.TrimSpace(req.Key))
	req.Name = strings.TrimSpace(req.Name)
	req.NameEn = strings.TrimSpace(req.NameEn)
	req.Description = strings.TrimSpace(req.Description)
	req.DescriptionEn = strings.TrimSpace(req.DescriptionEn)
	req.LockedHint = strings.TrimSpace(req.LockedHint)
	req.LockedHintEn = strings.TrimSpace(req.LockedHintEn)
	req.SeriesKey = strings.TrimSpace(req.SeriesKey)
	if !achievementKeyPattern.MatchString(req.Key) {
		return fmt.Errorf("成就标识只能使用小写字母、数字、点、下划线和中划线，长度为 3-80")
	}
	if req.Name == "" || len([]rune(req.Name)) > 120 {
		return fmt.Errorf("中文名称不能为空且不能超过 120 个字符")
	}
	if len([]rune(req.NameEn)) > 120 || len([]rune(req.Description)) > 500 || len([]rune(req.DescriptionEn)) > 500 || len([]rune(req.LockedHint)) > 255 || len([]rune(req.LockedHintEn)) > 255 || len([]rune(req.SeriesKey)) > 80 {
		return fmt.Errorf("成就文案或系列标识超过长度限制")
	}
	if req.Category == "" {
		req.Category = "reading"
	}
	if !oneOf(req.Category, "reading", "creation", "community", "account", "special") {
		return fmt.Errorf("成就分类无效")
	}
	if req.Status == "" {
		req.Status = "draft"
	}
	if !oneOf(req.Status, "draft", "active", "paused", "archived") {
		return fmt.Errorf("成就状态无效")
	}
	if req.Rarity == "" {
		req.Rarity = "common"
	}
	if !oneOf(req.Rarity, "common", "rare", "epic", "legendary") {
		return fmt.Errorf("稀有度无效")
	}
	if req.IconType == "" {
		req.IconType = "fa"
	}
	if !oneOf(req.IconType, "fa", "image", "svg") {
		return fmt.Errorf("图标类型无效")
	}
	if req.IconType == "fa" {
		if req.IconValue == "" {
			req.IconValue = "fa-trophy"
		}
		if !regexp.MustCompile(`^fa-[a-z0-9-]+$`).MatchString(req.IconValue) {
			return fmt.Errorf("Font Awesome 图标名无效")
		}
	} else if req.AssetID == nil || *req.AssetID == 0 {
		return fmt.Errorf("请先上传成就图标")
	}
	if req.RuleLogic == "" {
		req.RuleLogic = "all"
	}
	if !oneOf(req.RuleLogic, "all", "any") {
		return fmt.Errorf("规则组合方式无效")
	}
	if req.GrantMode == "" {
		req.GrantMode = "auto"
	}
	if !oneOf(req.GrantMode, "auto", "manual") {
		return fmt.Errorf("授予方式无效")
	}
	if req.GrantMode == "manual" {
		req.Rules = nil
	}
	if req.Visibility == "" {
		req.Visibility = "public"
	}
	if !oneOf(req.Visibility, "public", "private", "hidden") {
		return fmt.Errorf("可见性无效")
	}
	if req.ProgressMode == "" {
		req.ProgressMode = "aggregate"
	}
	if !oneOf(req.ProgressMode, "aggregate", "primary", "hidden") {
		return fmt.Errorf("进度展示方式无效")
	}
	if req.RewardXP < 0 || req.RewardXP > maxRewardXP {
		return fmt.Errorf("奖励经验需在 0 到 %d 之间", maxRewardXP)
	}
	if req.Tier < 1 {
		req.Tier = 1
	}
	if req.ActiveFrom != nil && req.ActiveUntil != nil && !req.ActiveUntil.After(*req.ActiveFrom) {
		return fmt.Errorf("失效时间必须晚于生效时间")
	}
	if req.GrantMode == "auto" && len(req.Rules) == 0 && req.Status != "draft" {
		return fmt.Errorf("自动成就启用前至少需要一条规则")
	}
	if len(req.Rules) > 10 {
		return fmt.Errorf("单个成就最多配置 10 条规则")
	}
	for i := range req.Rules {
		if err := normalizeAchievementRule(&req.Rules[i]); err != nil {
			return fmt.Errorf("第 %d 条规则：%w", i+1, err)
		}
	}
	return nil
}

func normalizeAchievementRule(rule *achievementRuleRequest) error {
	metric, exists := metricByKey(rule.MetricKey)
	if !exists {
		return fmt.Errorf("指标不存在")
	}
	if rule.Operator == "" {
		rule.Operator = "gte"
	}
	if !oneOf(rule.Operator, "gte", "eq", "between") {
		return fmt.Errorf("比较方式无效")
	}
	if rule.TargetValue < 1 {
		return fmt.Errorf("目标值必须大于 0")
	}
	if rule.Operator == "between" && rule.TargetMax < rule.TargetValue {
		return fmt.Errorf("区间上限不能小于下限")
	}
	if rule.WindowType == "" {
		rule.WindowType = "lifetime"
	}
	if !oneOf(rule.WindowType, metric.Windows...) {
		return fmt.Errorf("该指标不支持所选时间窗口")
	}
	if rule.WindowType == "rolling_days" {
		if rule.WindowValue < 1 || rule.WindowValue > 3650 {
			return fmt.Errorf("滚动天数必须为 1-3650")
		}
	} else {
		rule.WindowValue = 0
	}
	allowed := map[string]bool{}
	for _, key := range metric.AllowedFilters {
		allowed[key] = true
	}
	for key := range rule.Filters {
		if !allowed[key] {
			return fmt.Errorf("过滤条件 %s 不适用于该指标", key)
		}
	}
	raw, err := json.Marshal(rule.Filters)
	if err != nil {
		return fmt.Errorf("过滤条件格式无效")
	}
	var filters achievementFilterValues
	if err := json.Unmarshal(raw, &filters); err != nil {
		return fmt.Errorf("过滤条件类型无效")
	}
	for _, kind := range filters.Kind {
		if !oneOf(kind, "highlight", "note", "bookmark") {
			return fmt.Errorf("标注类型过滤条件无效")
		}
	}
	for _, status := range filters.Status {
		if !oneOf(status, "draft", "in_progress", "published", "completed", "archived") {
			return fmt.Errorf("内容状态过滤条件无效")
		}
	}
	return nil
}

func definitionFromRequest(req achievementDefinitionRequest, userID uint) models.AchievementDefinition {
	return models.AchievementDefinition{
		Key: req.Key, Name: req.Name, NameEn: req.NameEn, Description: req.Description,
		DescriptionEn: req.DescriptionEn, LockedHint: req.LockedHint, LockedHintEn: req.LockedHintEn,
		Category: req.Category, Status: req.Status, Rarity: req.Rarity, IconType: req.IconType,
		IconValue: req.IconValue, AssetID: req.AssetID, SeriesKey: req.SeriesKey, Tier: req.Tier, RewardXP: req.RewardXP,
		SupersedesPrevious: req.SupersedesPrevious, RuleLogic: req.RuleLogic, GrantMode: req.GrantMode,
		Visibility: req.Visibility, ProgressMode: req.ProgressMode, ActiveFrom: req.ActiveFrom,
		ActiveUntil: req.ActiveUntil, SortOrder: req.SortOrder, Version: 1, CreatedBy: userID, UpdatedBy: userID,
	}
}

func rulesFromRequest(id uint, requests []achievementRuleRequest) ([]models.AchievementRule, error) {
	rules := make([]models.AchievementRule, 0, len(requests))
	for index, req := range requests {
		raw, err := json.Marshal(req.Filters)
		if err != nil {
			return nil, err
		}
		rules = append(rules, models.AchievementRule{
			AchievementID: id, MetricKey: req.MetricKey, Operator: req.Operator,
			TargetValue: req.TargetValue, TargetMax: req.TargetMax, WindowType: req.WindowType,
			WindowValue: req.WindowValue, DistinctBy: req.DistinctBy, Filters: string(raw), SortOrder: index,
		})
	}
	return rules, nil
}

func (am *behavior) saveAchievementDefinitionVersion(tx *gorm.DB, definition models.AchievementDefinition, rules []models.AchievementRule, actorID uint) error {
	if err := am.attachAchievementTranslations(tx, &definition); err != nil {
		return err
	}
	definition.Rules = rules
	definition.Asset = nil
	raw, err := json.Marshal(definition)
	if err != nil {
		return err
	}
	version := models.AchievementDefinitionVersion{AchievementID: definition.ID, Version: definition.Version, Snapshot: string(raw), CreatedBy: actorID}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "achievement_id"}, {Name: "version"}}, DoNothing: true,
	}).Create(&version).Error
}

func (am *behavior) validateAchievementAsset(req *achievementDefinitionRequest) error {
	if req.IconType == "fa" {
		req.AssetID = nil
		return nil
	}
	var asset models.AchievementAsset
	if req.AssetID == nil || am.core.Gorm().First(&asset, *req.AssetID).Error != nil {
		return fmt.Errorf("成就图标资源不存在")
	}
	if req.IconType != asset.Kind {
		return fmt.Errorf("图标类型与上传资源不匹配")
	}
	req.IconValue = asset.URL
	return nil
}

// AdminListAchievements GET /admin/achievements。
func (am *behavior) AdminListAchievements(c *gin.Context) {
	page, pageSize := am.core.Paginate(c)
	query := am.core.Gorm().Model(&models.AchievementDefinition{})
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		query = query.Where("name LIKE ? OR achievement_key LIKE ?", "%"+q+"%", "%"+q+"%")
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		query = query.Where("status = ?", status)
	}
	if category := strings.TrimSpace(c.Query("category")); category != "" {
		query = query.Where("category = ?", category)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		am.core.Fail(c, http.StatusInternalServerError, "查询成就失败")
		return
	}
	items := []models.AchievementDefinition{}
	if err := query.Preload("Rules", func(db *gorm.DB) *gorm.DB { return db.Order("sort_order ASC, id ASC") }).Preload("Asset").
		Order("sort_order ASC, id DESC").Limit(pageSize).Offset((page - 1) * pageSize).Find(&items).Error; err != nil {
		am.core.Fail(c, http.StatusInternalServerError, "查询成就失败")
		return
	}
	for index := range items {
		if err := am.attachAchievementTranslations(am.core.Gorm(), &items[index]); err != nil {
			am.core.Fail(c, 500, "读取翻译失败")
			return
		}
		if items[index].Rules == nil {
			items[index].Rules = []models.AchievementRule{}
		}
	}
	am.core.OK(c, plugincore.PageResult{Items: items, Total: total, Page: page, PageSize: pageSize})
}

func (am *behavior) AdminGetAchievement(c *gin.Context) {
	var definition models.AchievementDefinition
	if err := am.core.Gorm().Preload("Rules", func(db *gorm.DB) *gorm.DB { return db.Order("sort_order ASC, id ASC") }).Preload("Asset").First(&definition, c.Param("id")).Error; err != nil {
		am.core.Fail(c, http.StatusNotFound, "成就不存在")
		return
	}
	if definition.Rules == nil {
		definition.Rules = []models.AchievementRule{}
	}
	if err := am.attachAchievementTranslations(am.core.Gorm(), &definition); err != nil {
		am.core.Fail(c, 500, "读取翻译失败")
		return
	}
	am.core.OK(c, definition)
}

func (am *behavior) AdminCreateAchievement(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var req achievementDefinitionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		am.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if err := am.prepareAchievementTranslations(&req, 0); err != nil {
		am.core.Fail(c, 400, err.Error())
		return
	}
	if err := normalizeAchievementRequest(&req); err != nil {
		am.core.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := am.validateAchievementAsset(&req); err != nil {
		am.core.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	user := am.core.CurrentUser(c)
	definition := definitionFromRequest(req, user.ID)
	err := am.core.Gorm().Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&definition).Error; err != nil {
			return err
		}
		if err := am.core.SaveResourceTranslations(tx, "achievement", definition.ID, user.ID, req.Translations); err != nil {
			return err
		}
		rules, err := rulesFromRequest(definition.ID, req.Rules)
		if err != nil {
			return err
		}
		if len(rules) > 0 {
			if err := tx.Create(&rules).Error; err != nil {
				return err
			}
		}
		return am.saveAchievementDefinitionVersion(tx, definition, rules, user.ID)
	})
	if err != nil {
		am.core.Fail(c, http.StatusBadRequest, "创建失败，成就标识可能已经存在")
		return
	}
	am.core.RecordAudit(c, "achievement.created", "achievement", auditID(definition.ID), definition.Name, map[string]any{"key": definition.Key, "status": definition.Status})
	if definition.Status == "active" && definition.GrantMode == "auto" && am.achievementSettings().Enabled {
		_, _ = am.enqueueAchievementRecalculation(definition.ID)
	}
	am.AdminGetAchievement(withParam(c, "id", strconv.FormatUint(uint64(definition.ID), 10)))
}

// withParam 只用于同一请求内复用读取 handler。
func withParam(c *gin.Context, key, value string) *gin.Context {
	for index := range c.Params {
		if c.Params[index].Key == key {
			c.Params[index].Value = value
			return c
		}
	}
	c.Params = append(c.Params, gin.Param{Key: key, Value: value})
	return c
}

func (am *behavior) AdminUpdateAchievement(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var definition models.AchievementDefinition
	if err := am.core.Gorm().First(&definition, c.Param("id")).Error; err != nil {
		am.core.Fail(c, http.StatusNotFound, "成就不存在")
		return
	}
	var req achievementDefinitionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		am.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if definition.Status != "draft" {
		req.Key = definition.Key
	}
	if err := am.prepareAchievementTranslations(&req, definition.ID); err != nil {
		am.core.Fail(c, 400, err.Error())
		return
	}
	if err := normalizeAchievementRequest(&req); err != nil {
		am.core.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := am.validateAchievementAsset(&req); err != nil {
		am.core.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	user := am.core.CurrentUser(c)
	version := definition.Version
	if definition.Status != "draft" || req.Status != "draft" {
		version++
	}
	updates := definitionFromRequest(req, definition.CreatedBy)
	updates.Version = version
	updates.UpdatedBy = user.ID
	err := am.core.Gorm().Transaction(func(tx *gorm.DB) error {
		if err := am.core.SaveResourceTranslations(tx, "achievement", definition.ID, user.ID, req.Translations); err != nil {
			return err
		}
		if err := tx.Model(&definition).Select(
			"achievement_key", "name", "name_en", "description", "description_en", "locked_hint", "locked_hint_en",
			"category", "status", "rarity", "icon_type", "icon_value", "asset_id", "series_key", "tier", "reward_xp",
			"supersedes_previous", "rule_logic", "grant_mode", "visibility", "progress_mode", "active_from",
			"active_until", "sort_order", "version", "updated_by",
		).Updates(&updates).Error; err != nil {
			return err
		}
		if err := tx.Where("achievement_id = ?", definition.ID).Delete(&models.AchievementRule{}).Error; err != nil {
			return err
		}
		rules, err := rulesFromRequest(definition.ID, req.Rules)
		if err != nil {
			return err
		}
		if len(rules) > 0 {
			if err := tx.Create(&rules).Error; err != nil {
				return err
			}
		}
		updates.ID = definition.ID
		updates.CreatedAt = definition.CreatedAt
		updates.UpdatedAt = currentTime()
		return am.saveAchievementDefinitionVersion(tx, updates, rules, user.ID)
	})
	if err != nil {
		if err == plugincore.ErrTranslationConflict {
			am.core.Fail(c, 409, err.Error())
		} else {
			am.core.Fail(c, 400, "保存成就失败："+err.Error())
		}
		return
	}
	am.core.RecordAudit(c, "achievement.updated", "achievement", auditID(definition.ID), req.Name, map[string]any{"version": version, "status": req.Status})
	if req.Status == "active" && am.achievementSettings().Enabled {
		_, _ = am.enqueueAchievementRecalculation(definition.ID)
	}
	am.AdminGetAchievement(c)
}

func (am *behavior) AdminDeleteAchievement(c *gin.Context) {
	var definition models.AchievementDefinition
	if err := am.core.Gorm().First(&definition, c.Param("id")).Error; err != nil {
		am.core.Fail(c, http.StatusNotFound, "成就不存在")
		return
	}
	var grants int64
	am.core.Gorm().Model(&models.UserAchievement{}).Where("achievement_id = ?", definition.ID).Count(&grants)
	if definition.Status == "draft" && grants == 0 {
		if err := am.core.Gorm().Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("achievement_id = ?", definition.ID).Delete(&models.AchievementRule{}).Error; err != nil {
				return err
			}
			if err := tx.Where("achievement_id = ?", definition.ID).Delete(&models.UserAchievementProgress{}).Error; err != nil {
				return err
			}
			if err := tx.Where("achievement_id = ?", definition.ID).Delete(&models.AchievementDefinitionVersion{}).Error; err != nil {
				return err
			}
			if err := tx.Where("resource_type = ? AND resource_id = ?", "achievement", definition.ID).Delete(&models.LocalizedResourceContent{}).Error; err != nil {
				return err
			}
			return tx.Delete(&definition).Error
		}); err != nil {
			am.core.Fail(c, http.StatusInternalServerError, "删除成就失败")
			return
		}
	} else if err := am.core.Gorm().Model(&definition).Updates(map[string]any{"status": "archived", "updated_by": am.core.CurrentUser(c).ID}).Error; err != nil {
		am.core.Fail(c, http.StatusInternalServerError, "归档成就失败")
		return
	}
	am.core.RecordAudit(c, "achievement.archived", "achievement", auditID(definition.ID), definition.Name, map[string]any{"deleted_draft": definition.Status == "draft" && grants == 0})
	am.core.OK(c, gin.H{"message": "成就已归档或删除"})
}

type achievementFilterValues struct {
	Kind        []string `json:"kind"`
	Status      []string `json:"status"`
	IsPublic    *bool    `json:"is_public"`
	RepliesOnly *bool    `json:"replies_only"`
}

func ruleFilters(raw string) achievementFilterValues {
	var filters achievementFilterValues
	_ = json.Unmarshal([]byte(raw), &filters)
	return filters
}

func achievementWindowStart(rule models.AchievementRule) *time.Time {
	now := currentTime()
	var start time.Time
	switch rule.WindowType {
	case "calendar_day":
		start = dayStart(now)
	case "calendar_week":
		start = weekStart(now)
	case "calendar_month":
		local := now.In(time.Local)
		start = time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, time.Local)
	case "rolling_days":
		start = dayStart(now).AddDate(0, 0, -(rule.WindowValue - 1))
	default:
		return nil
	}
	return &start
}

func applyAchievementWindow(query *gorm.DB, column string, rule models.AchievementRule) *gorm.DB {
	if start := achievementWindowStart(rule); start != nil {
		return query.Where(column+" >= ?", *start)
	}
	return query
}

func applyStringFilter(query *gorm.DB, column string, values []string) *gorm.DB {
	if len(values) > 0 {
		return query.Where(column+" IN ?", values)
	}
	return query
}

func (am *behavior) evaluateAchievementMetric(userID uint, rule models.AchievementRule) (int64, error) {
	filters := ruleFilters(rule.Filters)
	var value int64
	var err error
	switch rule.MetricKey {
	case "reading.chapters_read":
		q := applyAchievementWindow(am.core.Gorm().Model(&models.ReadChapter{}).Where("user_id = ?", userID), "created_at", rule)
		err = q.Count(&value).Error
	case "reading.books_started":
		q := applyAchievementWindow(am.core.Gorm().Model(&models.ReadingProgress{}).Where("user_id = ?", userID), "created_at", rule)
		err = q.Distinct("book_id").Count(&value).Error
	case "reading.minutes":
		q := am.core.Gorm().Model(&models.ReadingDailyTime{}).Where("user_id = ?", userID)
		if start := achievementWindowStart(rule); start != nil {
			q = q.Where("day >= ?", start.Format("2006-01-02"))
		}
		err = q.Select("COALESCE(SUM(seconds), 0)").Scan(&value).Error
		value /= 60
	case "reading.annotations":
		q := applyAchievementWindow(am.core.Gorm().Model(&models.ReadingAnnotation{}).Where("user_id = ?", userID), "created_at", rule)
		q = applyStringFilter(q, "kind", filters.Kind)
		err = q.Count(&value).Error
	case "reading.reading_days":
		q := am.core.Gorm().Model(&models.ReadingDailyTime{}).Where("user_id = ? AND seconds > 0", userID)
		if start := achievementWindowStart(rule); start != nil {
			q = q.Where("day >= ?", start.Format("2006-01-02"))
		}
		err = q.Distinct("day").Count(&value).Error
	case "social.likes_given", "social.favorites_given":
		typeName := "like"
		if rule.MetricKey == "social.favorites_given" {
			typeName = "favorite"
		}
		q := applyAchievementWindow(am.core.Gorm().Model(&models.Reaction{}).Where("user_id = ? AND type = ?", userID, typeName), "created_at", rule)
		err = q.Count(&value).Error
	case "social.comments_created":
		q := applyAchievementWindow(am.core.Gorm().Model(&models.Comment{}).Where("user_id = ? AND status = ?", userID, "published"), "created_at", rule)
		if filters.RepliesOnly != nil && *filters.RepliesOnly {
			q = q.Where("parent_id IS NOT NULL")
		}
		err = q.Count(&value).Error
	case "creator.books_created":
		q := applyAchievementWindow(am.core.Gorm().Model(&models.Book{}).Where("user_id = ?", userID), "created_at", rule)
		q = applyStringFilter(q, "status", filters.Status)
		if filters.IsPublic != nil {
			q = q.Where("is_public = ?", *filters.IsPublic)
		}
		err = q.Count(&value).Error
	case "creator.documents_created":
		q := applyAchievementWindow(am.core.Gorm().Model(&models.Document{}).Where("user_id = ?", userID), "created_at", rule)
		q = applyStringFilter(q, "status", filters.Status)
		err = q.Count(&value).Error
	case "creator.published_books":
		q := applyAchievementWindow(am.core.Gorm().Model(&models.Book{}).Where("user_id = ? AND status = ?", userID, "published"), "created_at", rule)
		err = q.Count(&value).Error
	case "creator.tags_used":
		// 标签表由标签插件建；未启用/已清除时该指标记 0
		if am.core.PluginEnabled(plugins.KeyTags) && am.core.Gorm().Migrator().HasTable("book_tags") {
			err = am.core.Gorm().Table("book_tags").
				Joins("JOIN books ON books.id = book_tags.book_id AND books.deleted_at IS NULL").
				Where("books.user_id = ?", userID).
				Distinct("book_tags.tag_id").Count(&value).Error
		}
	case "creator.published_words":
		err = am.core.Gorm().Model(&models.Document{}).
			Joins("JOIN books ON books.id = documents.book_id AND books.deleted_at IS NULL").
			Where("books.user_id = ? AND documents.status = ? AND documents.deleted_at IS NULL", userID, "published").
			Select("COALESCE(SUM(LENGTH(documents.content)), 0)").Scan(&value).Error
	case "reading.books_completed":
		err = am.core.Gorm().Raw(`SELECT COUNT(*) FROM (
			SELECT rc.book_id FROM read_chapters rc
			JOIN documents d ON d.id = rc.doc_id AND d.deleted_at IS NULL AND d.status = ?
			WHERE rc.user_id = ?
			GROUP BY rc.book_id
			HAVING COUNT(DISTINCT rc.doc_id) >= (
				SELECT COUNT(*) FROM documents pd WHERE pd.book_id = rc.book_id AND pd.deleted_at IS NULL AND pd.status = ?
			)
		) completed`, "published", userID, "published").Scan(&value).Error
	case "growth.total_xp", "growth.current_level":
		// 成长指标：需启用成长插件且表存在，否则记 0
		if am.core.PluginEnabled(plugins.KeyGrowth) && am.core.Gorm().Migrator().HasTable("user_growth_profiles") {
			var p models.UserGrowthProfile
			if am.core.Gorm().Where("user_id = ?", userID).First(&p).Error == nil {
				if rule.MetricKey == "growth.total_xp" {
					value = p.LifetimeXP
				} else {
					value = int64(p.CurrentLevel)
				}
			}
		}
	case "checkin.total_days", "checkin.current_streak", "checkin.longest_streak":
		// 签到指标：签到表由成长插件建，需启用成长插件且表存在，否则记 0
		if am.core.PluginEnabled(plugins.KeyGrowth) && am.core.Gorm().Migrator().HasTable(&models.UserCheckin{}) {
			db := am.core.Gorm().Model(&models.UserCheckin{}).Where("user_id = ?", userID)
			switch rule.MetricKey {
			case "checkin.total_days":
				err = applyAchievementWindow(db, "created_at", rule).Count(&value).Error
			case "checkin.longest_streak":
				err = db.Select("COALESCE(MAX(streak),0)").Scan(&value).Error
			default:
				// 最近一次签到是今天或昨天时连续未中断
				var last models.UserCheckin
				if db.Order("day DESC").First(&last).Error == nil {
					now := currentTime().In(time.Local)
					if last.Day == now.Format("2006-01-02") || last.Day == now.AddDate(0, 0, -1).Format("2006-01-02") {
						value = int64(last.Streak)
					}
				}
			}
		}
	case "creator.views_received":
		q := am.core.Gorm().Model(&models.Book{}).Where("user_id = ?", userID)
		q = applyStringFilter(q, "status", filters.Status)
		if filters.IsPublic != nil {
			q = q.Where("is_public = ?", *filters.IsPublic)
		}
		err = q.Select("COALESCE(SUM(view_count), 0)").Scan(&value).Error
	case "creator.likes_received", "creator.favorites_received":
		typeName := "like"
		if rule.MetricKey == "creator.favorites_received" {
			typeName = "favorite"
		}
		q := am.core.Gorm().Model(&models.Reaction{}).Joins("JOIN books ON books.id = reactions.book_id AND books.deleted_at IS NULL").
			Where("books.user_id = ? AND reactions.type = ?", userID, typeName)
		q = applyAchievementWindow(q, "reactions.created_at", rule)
		err = q.Count(&value).Error
	case "creator.comments_received":
		q := am.core.Gorm().Model(&models.Comment{}).
			Joins("JOIN documents ON documents.id = comments.document_id AND documents.deleted_at IS NULL").
			Joins("JOIN books ON books.id = documents.book_id AND books.deleted_at IS NULL").
			Where("books.user_id = ? AND comments.status = ?", userID, "published")
		if filters.RepliesOnly != nil && *filters.RepliesOnly {
			q = q.Where("comments.parent_id IS NOT NULL")
		}
		q = applyAchievementWindow(q, "comments.created_at", rule)
		err = q.Count(&value).Error
	case "account.age_days":
		var user models.User
		if err = am.core.Gorm().Select("created_at").First(&user, userID).Error; err == nil {
			value = int64(currentTime().Sub(user.CreatedAt).Hours() / 24)
			if value < 0 {
				value = 0
			}
		}
	case "account.invited_users":
		q := applyAchievementWindow(am.core.Gorm().Model(&models.User{}).Where("invited_by = ?", userID), "created_at", rule)
		err = q.Count(&value).Error
	case "account.oauth_bindings":
		err = am.core.Gorm().Model(&models.UserAuthentication{}).Where("user_id = ?", userID).Count(&value).Error
	case "account.email_verified", "account.two_factor_enabled":
		var user models.User
		if err = am.core.Gorm().Select("email_verified", "two_factor_enabled").First(&user, userID).Error; err == nil {
			if (rule.MetricKey == "account.email_verified" && user.EmailVerified) || (rule.MetricKey == "account.two_factor_enabled" && user.TwoFactorEnabled) {
				value = 1
			}
		}
	default:
		m, ok := plugincore.UserMetricByKey(rule.MetricKey)
		if !ok {
			err = fmt.Errorf("不支持的成就指标")
		} else if m.Available == nil || m.Available(am.core) {
			value, err = m.Value(am.core, userID, achievementWindowStart(rule))
		}
	}
	return value, err
}

type achievementRuleValue struct {
	RuleID      uint   `json:"rule_id"`
	MetricKey   string `json:"metric_key"`
	Value       int64  `json:"value"`
	TargetValue int64  `json:"target_value"`
	TargetMax   int64  `json:"target_max,omitempty"`
	Met         bool   `json:"met"`
	Percent     int    `json:"percent"`
}

func evaluateRuleValue(rule models.AchievementRule, value int64) achievementRuleValue {
	met := false
	switch rule.Operator {
	case "eq":
		met = value == rule.TargetValue
	case "between":
		met = value >= rule.TargetValue && value <= rule.TargetMax
	default:
		met = value >= rule.TargetValue
	}
	percent := int(math.Round(float64(value) / float64(rule.TargetValue) * 100))
	if percent < 0 {
		percent = 0
	} else if percent > 100 {
		percent = 100
	}
	return achievementRuleValue{RuleID: rule.ID, MetricKey: rule.MetricKey, Value: value, TargetValue: rule.TargetValue, TargetMax: rule.TargetMax, Met: met, Percent: percent}
}

func achievementIsEffective(definition models.AchievementDefinition, now time.Time) bool {
	if definition.Status != "active" {
		return false
	}
	if definition.ActiveFrom != nil && now.Before(*definition.ActiveFrom) {
		return false
	}
	return definition.ActiveUntil == nil || !now.After(*definition.ActiveUntil)
}

func (am *behavior) evaluateAchievementForUser(userID uint, definition models.AchievementDefinition) error {
	if definition.GrantMode != "auto" || !achievementIsEffective(definition, currentTime()) || len(definition.Rules) == 0 {
		return nil
	}
	values := make([]achievementRuleValue, 0, len(definition.Rules))
	allMet, anyMet, percentSum, maxPercent := true, false, 0, 0
	for _, rule := range definition.Rules {
		value, err := am.evaluateAchievementMetric(userID, rule)
		if err != nil {
			return err
		}
		result := evaluateRuleValue(rule, value)
		values = append(values, result)
		allMet = allMet && result.Met
		anyMet = anyMet || result.Met
		percentSum += result.Percent
		if result.Percent > maxPercent {
			maxPercent = result.Percent
		}
	}
	unlocked := allMet
	percent := percentSum / len(values)
	if definition.RuleLogic == "any" {
		unlocked = anyMet
		percent = maxPercent
	}
	if definition.ProgressMode == "primary" {
		percent = values[0].Percent
	}
	raw, _ := json.Marshal(values)
	now := currentTime()
	createdGrant := false
	err := am.core.Gorm().Transaction(func(tx *gorm.DB) error {
		// 时间字段须有值：MySQL 严格模式拒绝零值日期 '0000-00-00'（SQLite 不报错，测试覆盖不到）
		progress := models.UserAchievementProgress{UserID: userID, AchievementID: definition.ID, DefinitionVersion: definition.Version, LastEvaluatedAt: now}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "user_id"}, {Name: "achievement_id"}}, DoNothing: true,
		}).Create(&progress).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ? AND achievement_id = ?", userID, definition.ID).First(&progress).Error; err != nil {
			return err
		}
		status := "pending"
		if unlocked {
			status = "unlocked"
		}
		if err := tx.Model(&progress).Updates(map[string]any{
			"definition_version": definition.Version, "current_value": values[0].Value,
			"percent": percent, "rule_values": string(raw), "status": status, "last_evaluated_at": now,
		}).Error; err != nil {
			return err
		}
		if !unlocked {
			return nil
		}
		grant := models.UserAchievement{
			UserID: userID, AchievementID: definition.ID, DefinitionVersion: definition.Version,
			Source: "auto", MetricsSnapshot: string(raw), IsPublic: definition.Visibility != "private", UnlockedAt: now,
		}
		result := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "user_id"}, {Name: "achievement_id"}}, DoNothing: true,
		}).Create(&grant)
		if result.Error != nil {
			return result.Error
		}
		createdGrant = result.RowsAffected > 0
		return nil
	})
	if err != nil {
		return err
	}
	if createdGrant && am.achievementSettings().NotificationsEnabled {
		am.core.NotifyI18n(userID, "achievement", "notify.achievement.unlocked", map[string]string{"name": definition.Name}, map[string]any{"link": "/user/achievements", "achievement_key": definition.Key})
		am.core.Gorm().Model(&models.UserAchievement{}).Where("user_id = ? AND achievement_id = ?", userID, definition.ID).Update("notified_at", now)
	}
	// 成长联动：成就解锁奖励经验（每个用户每个成就只持有一份；成长插件启用时生效）
	if createdGrant && definition.RewardXP > 0 {
		am.grantRewardXP(userID, definition)
	}
	return nil
}

func (am *behavior) evaluateAllAchievementsForUser(userID uint) error {
	if !am.achievementSettings().Enabled {
		return nil
	}
	definitions := []models.AchievementDefinition{}
	if err := am.core.Gorm().Preload("Rules", func(db *gorm.DB) *gorm.DB { return db.Order("sort_order ASC, id ASC") }).
		Where("status = ? AND grant_mode = ?", "active", "auto").Find(&definitions).Error; err != nil {
		return err
	}
	for _, definition := range definitions {
		if err := am.evaluateAchievementForUser(userID, definition); err != nil {
			return err
		}
	}
	return nil
}

// recordAchievementEvent 在业务操作成功后持久化一个幂等评估触发；失败不反向破坏主业务。
func (am *behavior) recordAchievementEvent(userID uint, eventType, sourceType, sourceID, dedupeKey string) {
	if userID == 0 || !am.achievementSettings().Enabled {
		return
	}
	event := models.AchievementEvent{UserID: userID, Type: eventType, SourceType: sourceType, SourceID: sourceID, DedupeKey: dedupeKey}
	result := am.core.Gorm().Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "dedupe_key"}}, DoNothing: true}).Create(&event)
	if result.Error != nil || result.RowsAffected == 0 {
		return
	}
	if queue := am.core.JobQueue(); queue != nil {
		am.enqueueAchievementEvent(queue, event.ID)
	}
}

type userAchievementItem struct {
	Definition models.AchievementDefinition    `json:"definition"`
	Progress   *models.UserAchievementProgress `json:"progress"`
	Grant      *models.UserAchievement         `json:"grant"`
	Unlocked   bool                            `json:"unlocked"`
}

func (am *behavior) MyAchievements(c *gin.Context) {
	settings := am.achievementSettings()
	if !settings.Enabled {
		am.core.OK(c, gin.H{"enabled": false, "allow_user_hide": settings.AllowUserHide, "items": []userAchievementItem{}, "unlocked_count": 0, "total": 0})
		return
	}
	user := am.core.CurrentUser(c)
	// 打开「我的成就」时实时评估一次：刷新进度、并补授已达成但因事件未触发而尚未解锁的成就
	// （保证进度条新鲜、「看到即解锁」）。失败不阻塞页面。
	_ = am.evaluateAllAchievementsForUser(user.ID)
	definitions := []models.AchievementDefinition{}
	if err := am.core.Gorm().Preload("Rules", func(db *gorm.DB) *gorm.DB { return db.Order("sort_order ASC, id ASC") }).Preload("Asset").
		Where("status = ?", "active").Order("sort_order ASC, id ASC").Find(&definitions).Error; err != nil {
		am.core.Fail(c, http.StatusInternalServerError, "获取成就失败")
		return
	}
	progresses := []models.UserAchievementProgress{}
	if err := am.localizeAchievements(c, definitions); err != nil {
		am.core.Fail(c, 500, "读取翻译失败")
		return
	}
	am.core.Gorm().Where("user_id = ?", user.ID).Find(&progresses)
	progressByID := map[uint]*models.UserAchievementProgress{}
	for index := range progresses {
		progressByID[progresses[index].AchievementID] = &progresses[index]
	}
	grants := []models.UserAchievement{}
	am.core.Gorm().Where("user_id = ? AND revoked_at IS NULL", user.ID).Find(&grants)
	grantByID := map[uint]*models.UserAchievement{}
	for index := range grants {
		grantByID[grants[index].AchievementID] = &grants[index]
	}
	items := make([]userAchievementItem, 0, len(definitions))
	unlockedCount := 0
	for _, definition := range definitions {
		grant := grantByID[definition.ID]
		unlocked := grant != nil
		if unlocked {
			unlockedCount++
		}
		progress := progressByID[definition.ID]
		if definition.Visibility == "hidden" && !unlocked {
			definition.Name = definition.LockedHint
			if definition.Name == "" {
				definition.Name = "—"
			}
			definition.NameEn = ""
			definition.Description = definition.LockedHint
			definition.DescriptionEn = definition.LockedHintEn
			definition.IconType = "fa"
			definition.IconValue = "fa-lock"
			definition.Asset = nil
			definition.AssetID = nil
			definition.Rules = []models.AchievementRule{}
			progress = nil
		}
		if definition.Rules == nil {
			definition.Rules = []models.AchievementRule{}
		}
		items = append(items, userAchievementItem{Definition: definition, Progress: progress, Grant: grant, Unlocked: unlocked})
	}
	am.core.OK(c, gin.H{"enabled": true, "allow_user_hide": settings.AllowUserHide, "items": items, "unlocked_count": unlockedCount, "total": len(items)})
}

func (am *behavior) UpdateMyAchievementDisplay(c *gin.Context) {
	settings := am.achievementSettings()
	if !settings.Enabled {
		am.core.Fail(c, http.StatusNotFound, "成就模块未开启")
		return
	}
	user := am.core.CurrentUser(c)
	var grant models.UserAchievement
	if err := am.core.Gorm().Preload("Achievement").Where("id = ? AND user_id = ? AND revoked_at IS NULL", c.Param("id"), user.ID).First(&grant).Error; err != nil {
		am.core.Fail(c, http.StatusNotFound, "成就不存在")
		return
	}
	var req struct {
		IsPublic      *bool `json:"is_public"`
		ShowcaseOrder *int  `json:"showcase_order"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		am.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	updates := map[string]any{}
	if req.IsPublic != nil {
		if *req.IsPublic && (grant.Achievement == nil || grant.Achievement.Visibility == "private") {
			am.core.Fail(c, http.StatusForbidden, "该成就定义为仅本人可见")
			return
		}
		if !settings.AllowUserHide && !*req.IsPublic {
			am.core.Fail(c, http.StatusForbidden, "管理员未开放隐藏成就功能")
			return
		}
		updates["is_public"] = *req.IsPublic
	}
	if req.ShowcaseOrder != nil {
		order := *req.ShowcaseOrder
		if order < 0 {
			order = 0
		} else if order > settings.ShowcaseLimit {
			order = settings.ShowcaseLimit
		}
		updates["showcase_order"] = order
	}
	if len(updates) > 0 {
		if err := am.core.Gorm().Model(&grant).Updates(updates).Error; err != nil {
			am.core.Fail(c, http.StatusInternalServerError, "保存展示设置失败")
			return
		}
	}
	am.core.Gorm().First(&grant, grant.ID)
	am.core.OK(c, grant)
}

type publicAchievementDefinition struct {
	ResolvedLocale string                   `json:"resolved_locale"`
	ID             uint                     `json:"id"`
	Key            string                   `json:"key"`
	Name           string                   `json:"name"`
	NameEn         string                   `json:"name_en"`
	Description    string                   `json:"description"`
	DescriptionEn  string                   `json:"description_en"`
	Category       string                   `json:"category"`
	Rarity         string                   `json:"rarity"`
	IconType       string                   `json:"icon_type"`
	IconValue      string                   `json:"icon_value"`
	Asset          *models.AchievementAsset `json:"asset,omitempty"`
	SeriesKey      string                   `json:"series_key"`
	Tier           int                      `json:"tier"`
}

type publicAchievementGrant struct {
	ID          uint                        `json:"id"`
	Achievement publicAchievementDefinition `json:"achievement"`
	UnlockedAt  time.Time                   `json:"unlocked_at"`
}

func (am *behavior) PublicUserAchievements(c *gin.Context) {
	settings := am.achievementSettings()
	if !settings.Enabled || !settings.PublicProfileEnabled {
		am.core.OK(c, gin.H{"enabled": false, "items": []models.UserAchievement{}})
		return
	}
	var user models.User
	if err := am.core.Gorm().Select("id").Where("username = ?", c.Param("username")).First(&user).Error; err != nil {
		am.core.Fail(c, http.StatusNotFound, "用户不存在")
		return
	}
	grants := []models.UserAchievement{}
	if err := am.core.Gorm().Preload("Achievement", func(db *gorm.DB) *gorm.DB { return db.Preload("Asset") }).
		Joins("JOIN achievement_definitions ON achievement_definitions.id = user_achievements.achievement_id").
		Where("user_achievements.user_id = ? AND user_achievements.is_public = ? AND user_achievements.revoked_at IS NULL AND achievement_definitions.visibility IN ?", user.ID, true, []string{"public", "hidden"}).
		Order("user_achievements.showcase_order DESC, user_achievements.unlocked_at DESC").Find(&grants).Error; err != nil {
		am.core.Fail(c, http.StatusInternalServerError, "获取公开成就失败")
		return
	}
	highestTier := map[string]int{}
	localized := []models.AchievementDefinition{}
	for _, grant := range grants {
		if grant.Achievement != nil {
			localized = append(localized, *grant.Achievement)
		}
	}
	if err := am.localizeAchievements(c, localized); err != nil {
		am.core.Fail(c, 500, "读取翻译失败")
		return
	}
	localizedByID := map[uint]models.AchievementDefinition{}
	for _, d := range localized {
		localizedByID[d.ID] = d
	}
	for i := range grants {
		if grants[i].Achievement != nil {
			d := localizedByID[grants[i].AchievementID]
			grants[i].Achievement = &d
		}
	}
	for _, grant := range grants {
		if grant.Achievement != nil && grant.Achievement.SeriesKey != "" && grant.Achievement.SupersedesPrevious && grant.Achievement.Tier > highestTier[grant.Achievement.SeriesKey] {
			highestTier[grant.Achievement.SeriesKey] = grant.Achievement.Tier
		}
	}
	items := make([]publicAchievementGrant, 0, len(grants))
	for _, grant := range grants {
		if grant.Achievement == nil {
			continue
		}
		definition := grant.Achievement
		if tier := highestTier[definition.SeriesKey]; definition.SeriesKey != "" && tier > definition.Tier {
			continue
		}
		items = append(items, publicAchievementGrant{ID: grant.ID, UnlockedAt: grant.UnlockedAt, Achievement: publicAchievementDefinition{
			ResolvedLocale: definition.ResolvedLocale,
			ID:             definition.ID, Key: definition.Key, Name: definition.Name, NameEn: definition.NameEn,
			Description: definition.Description, DescriptionEn: definition.DescriptionEn, Category: definition.Category,
			Rarity: definition.Rarity, IconType: definition.IconType, IconValue: definition.IconValue,
			Asset: definition.Asset, SeriesKey: definition.SeriesKey, Tier: definition.Tier,
		}})
		if len(items) >= settings.ShowcaseLimit {
			break
		}
	}
	am.core.OK(c, gin.H{"enabled": true, "items": items})
}

func (am *behavior) AdminListAchievementGrants(c *gin.Context) {
	page, pageSize := am.core.Paginate(c)
	query := am.core.Gorm().Model(&models.UserAchievement{})
	if userID := am.core.AtoiDefault(c.Query("user_id"), 0); userID > 0 {
		query = query.Where("user_id = ?", userID)
	}
	if achievementID := am.core.AtoiDefault(c.Query("achievement_id"), 0); achievementID > 0 {
		query = query.Where("achievement_id = ?", achievementID)
	}
	if c.Query("revoked") == "true" {
		query = query.Where("revoked_at IS NOT NULL")
	} else {
		query = query.Where("revoked_at IS NULL")
	}
	var total int64
	query.Count(&total)
	items := []models.UserAchievement{}
	if err := query.Preload("Achievement").Order("unlocked_at DESC, id DESC").Limit(pageSize).Offset((page - 1) * pageSize).Find(&items).Error; err != nil {
		am.core.Fail(c, http.StatusInternalServerError, "查询授予记录失败")
		return
	}
	userIDs := []uint{}
	for _, item := range items {
		userIDs = append(userIDs, item.UserID)
	}
	users := []models.User{}
	if len(userIDs) > 0 {
		am.core.Gorm().Select("id", "username", "nickname", "name_display", "avatar").Where("id IN ?", userIDs).Find(&users)
	}
	userMap := map[uint]models.User{}
	for _, user := range users {
		userMap[user.ID] = user
	}
	rows := make([]gin.H, 0, len(items))
	for _, item := range items {
		rows = append(rows, gin.H{"grant": item, "user": userMap[item.UserID]})
	}
	am.core.OK(c, plugincore.PageResult{Items: rows, Total: total, Page: page, PageSize: pageSize})
}

func (am *behavior) AdminGrantAchievement(c *gin.Context) {
	var req struct {
		Username      string `json:"username"`
		AchievementID uint   `json:"achievement_id"`
		Reason        string `json:"reason"`
		IsPublic      *bool  `json:"is_public"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Username) == "" || req.AchievementID == 0 {
		am.core.Fail(c, http.StatusBadRequest, "用户和成就不能为空")
		return
	}
	var user models.User
	if err := am.core.Gorm().Where("username = ?", strings.TrimSpace(req.Username)).First(&user).Error; err != nil {
		am.core.Fail(c, http.StatusNotFound, "用户不存在")
		return
	}
	var definition models.AchievementDefinition
	if err := am.core.Gorm().First(&definition, req.AchievementID).Error; err != nil {
		am.core.Fail(c, http.StatusNotFound, "成就不存在")
		return
	}
	if definition.Status != "active" {
		am.core.Fail(c, http.StatusBadRequest, "只能授予已启用的成就")
		return
	}
	public := definition.Visibility != "private"
	if req.IsPublic != nil && definition.Visibility != "private" {
		public = *req.IsPublic
	}
	now := currentTime()
	var grant models.UserAchievement
	created := false
	reactivated := false
	err := am.core.Gorm().Transaction(func(tx *gorm.DB) error {
		find := tx.Where("user_id = ? AND achievement_id = ?", user.ID, definition.ID).First(&grant)
		if find.Error == gorm.ErrRecordNotFound {
			grant = models.UserAchievement{UserID: user.ID, AchievementID: definition.ID, DefinitionVersion: definition.Version, Source: "manual", GrantorID: am.core.CurrentUser(c).ID, Reason: strings.TrimSpace(req.Reason), IsPublic: public, UnlockedAt: now}
			created = true
			return tx.Create(&grant).Error
		}
		if find.Error != nil {
			return find.Error
		}
		if grant.RevokedAt == nil {
			return nil
		}
		reactivated = true
		return tx.Model(&grant).Updates(map[string]any{"source": "manual", "grantor_id": am.core.CurrentUser(c).ID, "reason": strings.TrimSpace(req.Reason), "is_public": public, "unlocked_at": now, "revoked_at": nil, "revoked_by": 0, "revoke_reason": ""}).Error
	})
	if err != nil {
		am.core.Fail(c, http.StatusInternalServerError, "授予成就失败")
		return
	}
	am.core.RecordAudit(c, "achievement.granted", "achievement_grant", auditID(grant.ID), definition.Name, map[string]any{"user_id": user.ID, "achievement_id": definition.ID, "created": created, "reactivated": reactivated})
	if (created || reactivated) && am.achievementSettings().NotificationsEnabled {
		am.core.NotifyI18n(user.ID, "achievement", "notify.achievement.granted", map[string]string{"name": definition.Name}, map[string]any{"link": "/user/achievements", "achievement_key": definition.Key})
	}
	// 成长联动：手工授予同样奖励经验；每个用户每个成就只持有一份（撤销后重新授予可再得，不会重复）
	if (created || reactivated) && definition.RewardXP > 0 {
		am.grantRewardXP(user.ID, definition)
	}
	am.core.OK(c, grant)
}

func (am *behavior) AdminRevokeAchievement(c *gin.Context) {
	var grant models.UserAchievement
	if err := am.core.Gorm().Preload("Achievement").Where("id = ? AND revoked_at IS NULL", c.Param("id")).First(&grant).Error; err != nil {
		am.core.Fail(c, http.StatusNotFound, "授予记录不存在")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Reason) == "" {
		am.core.Fail(c, http.StatusBadRequest, "撤销原因不能为空")
		return
	}
	now := currentTime()
	if err := am.core.Gorm().Model(&grant).Updates(map[string]any{"revoked_at": now, "revoked_by": am.core.CurrentUser(c).ID, "revoke_reason": strings.TrimSpace(req.Reason)}).Error; err != nil {
		am.core.Fail(c, http.StatusInternalServerError, "撤销成就失败")
		return
	}
	am.core.RecordAudit(c, "achievement.revoked", "achievement_grant", auditID(grant.ID), grant.Achievement.Name, map[string]any{"user_id": grant.UserID, "reason": strings.TrimSpace(req.Reason)})
	// 成长联动：收回该成就的奖励经验（成长插件未启用时为空操作）
	plugincore.RevokeExperience(am.core, grant.UserID, rewardRuleKey, strconv.FormatUint(uint64(grant.AchievementID), 10), "achievement_revoked")
	am.core.OK(c, gin.H{"message": "成就已撤销"})
}

func (am *behavior) AdminRecalculateAchievement(c *gin.Context) {
	id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		am.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	var definition models.AchievementDefinition
	if err := am.core.Gorm().First(&definition, uint(id64)).Error; err != nil {
		am.core.Fail(c, http.StatusNotFound, "成就不存在")
		return
	}
	if definition.Status != "active" || definition.GrantMode != "auto" {
		am.core.Fail(c, http.StatusBadRequest, "只有已启用的自动成就可以重新计算")
		return
	}
	job, err := am.enqueueAchievementRecalculation(definition.ID)
	if err != nil {
		am.core.Fail(c, http.StatusServiceUnavailable, "成就重算任务暂不可用")
		return
	}
	am.core.RecordAudit(c, "achievement.recalculated", "achievement", auditID(definition.ID), definition.Name, map[string]any{"task_id": job.ID})
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": gin.H{"task": am.core.PublicBackgroundJob(job)}})
}

// 保证定义列表输出稳定，供测试和后续注册表扩展时比较。
func sortedAchievementMetricKeys() []string {
	keys := make([]string, 0, len(achievementMetrics))
	for _, metric := range achievementMetrics {
		keys = append(keys, metric.Key)
	}
	sort.Strings(keys)
	return keys
}
