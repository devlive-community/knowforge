package achievements

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// 预设成就：代码内置的一组常用成就（阅读、创作、互动、账号、签到、成长），覆盖各白名单指标的阶梯目标。
//   - 插件首次启用且尚无任何成就定义时自动安装全部预设（开箱即有可玩内容）；
//   - 之后管理员可在后台「预设成就」中查看并按需安装缺失的预设（已安装/同 key 已存在的跳过，不覆盖管理员的修改）。
// 预设与管理员手工创建的成就走同一套校验、多语言与版本快照流程，安装后即为普通成就，可自由编辑/停用/删除。

type presetText struct{ ZH, EN string }

type presetAchievement struct {
	Key       string
	Category  string
	Series    string
	Tier      int
	Rarity    string
	Icon      string
	RewardXP  int
	Metric    string
	Target    int64
	Name      presetText
	Desc      presetText
	SortOrder int
}

// p 以紧凑参数构造一条预设；描述统一为「目标说明」，锁定提示沿用描述。
func p(key, category, series string, tier int, rarity, icon string, xp int, metric string, target int64, zhName, enName, zhDesc, enDesc string) presetAchievement {
	return presetAchievement{
		Key: "preset." + key, Category: category, Series: series, Tier: tier, Rarity: rarity, Icon: icon, RewardXP: xp,
		Metric: metric, Target: target, Name: presetText{zhName, enName}, Desc: presetText{zhDesc, enDesc},
	}
}

var presetAchievements = func() []presetAchievement {
	list := []presetAchievement{
		// —— 阅读 ——
		p("reading.chapters.1", "reading", "preset.reading.chapters", 1, "common", "fa-book-open", 5, "reading.chapters_read", 1, "开卷有益", "First Page", "首次读完一个章节", "Read your first chapter"),
		p("reading.chapters.10", "reading", "preset.reading.chapters", 2, "common", "fa-book-open-reader", 10, "reading.chapters_read", 10, "渐入佳境", "Getting Into It", "累计阅读 10 个章节", "Read 10 chapters"),
		p("reading.chapters.50", "reading", "preset.reading.chapters", 3, "rare", "fa-book-bookmark", 30, "reading.chapters_read", 50, "手不释卷", "Page Turner", "累计阅读 50 个章节", "Read 50 chapters"),
		p("reading.chapters.200", "reading", "preset.reading.chapters", 4, "epic", "fa-graduation-cap", 80, "reading.chapters_read", 200, "博览群书", "Well Read", "累计阅读 200 个章节", "Read 200 chapters"),
		p("reading.chapters.1000", "reading", "preset.reading.chapters", 5, "legendary", "fa-crown", 200, "reading.chapters_read", 1000, "学富五车", "Voracious Reader", "累计阅读 1000 个章节", "Read 1,000 chapters"),
		p("reading.completed.1", "reading", "preset.reading.completed", 1, "common", "fa-flag-checkered", 20, "reading.books_completed", 1, "有始有终", "The End", "完整读完一本书", "Finish reading a whole book"),
		p("reading.completed.5", "reading", "preset.reading.completed", 2, "rare", "fa-medal", 50, "reading.books_completed", 5, "读书达人", "Finisher", "完整读完 5 本书", "Finish 5 books"),
		p("reading.completed.20", "reading", "preset.reading.completed", 3, "epic", "fa-trophy", 120, "reading.books_completed", 20, "书海遨游", "Bookworm", "完整读完 20 本书", "Finish 20 books"),
		p("reading.minutes.60", "reading", "preset.reading.minutes", 1, "common", "fa-clock", 10, "reading.minutes", 60, "专注一小时", "One Focused Hour", "累计阅读 60 分钟", "Read for 60 minutes in total"),
		p("reading.minutes.600", "reading", "preset.reading.minutes", 2, "rare", "fa-hourglass-half", 40, "reading.minutes", 600, "沉浸十小时", "Ten Hours Deep", "累计阅读 10 小时", "Read for 10 hours in total"),
		p("reading.minutes.3000", "reading", "preset.reading.minutes", 3, "epic", "fa-person-running", 100, "reading.minutes", 3000, "阅读马拉松", "Reading Marathon", "累计阅读 50 小时", "Read for 50 hours in total"),
		p("reading.days.7", "reading", "preset.reading.days", 1, "common", "fa-calendar-week", 15, "reading.reading_days", 7, "七日书香", "Week of Reading", "累计 7 天有阅读", "Read on 7 different days"),
		p("reading.days.30", "reading", "preset.reading.days", 2, "rare", "fa-calendar-days", 50, "reading.reading_days", 30, "阅读成习", "Reading Habit", "累计 30 天有阅读", "Read on 30 different days"),
		p("reading.days.100", "reading", "preset.reading.days", 3, "epic", "fa-seedling", 120, "reading.reading_days", 100, "百日书香", "Hundred Days of Reading", "累计 100 天有阅读", "Read on 100 different days"),
		p("reading.annotations.1", "reading", "preset.reading.annotations", 1, "common", "fa-highlighter", 5, "reading.annotations", 1, "初次批注", "First Note", "添加第一条划线、笔记或书签", "Add your first highlight, note or bookmark"),
		p("reading.annotations.50", "reading", "preset.reading.annotations", 2, "rare", "fa-pen-to-square", 40, "reading.annotations", 50, "勤记笔记", "Note Taker", "累计添加 50 条标注", "Add 50 annotations"),
		// —— 创作 ——
		p("creation.books.1", "creation", "", 1, "common", "fa-book", 10, "creator.books_created", 1, "第一本书", "My First Book", "创建你的第一本书", "Create your first book"),
		p("creation.published.1", "creation", "preset.creation.published", 1, "common", "fa-rocket", 20, "creator.published_books", 1, "正式出版", "Published!", "发布第一本书", "Publish your first book"),
		p("creation.published.5", "creation", "preset.creation.published", 2, "epic", "fa-layer-group", 100, "creator.published_books", 5, "多产作者", "Prolific Author", "发布 5 本书", "Publish 5 books"),
		p("creation.chapters.10", "creation", "preset.creation.chapters", 1, "common", "fa-feather", 15, "creator.documents_created", 10, "笔耕不辍", "Steady Writer", "累计创建 10 个章节", "Create 10 chapters"),
		p("creation.chapters.100", "creation", "preset.creation.chapters", 2, "epic", "fa-feather-pointed", 100, "creator.documents_created", 100, "著作等身", "Chapter Machine", "累计创建 100 个章节", "Create 100 chapters"),
		p("creation.words.10000", "creation", "preset.creation.words", 1, "common", "fa-file-lines", 20, "creator.published_words", 10000, "万字作者", "10K Words", "已发布章节累计 1 万字", "Publish 10,000 characters in total"),
		p("creation.words.100000", "creation", "preset.creation.words", 2, "epic", "fa-scroll", 100, "creator.published_words", 100000, "十万字作者", "100K Words", "已发布章节累计 10 万字", "Publish 100,000 characters in total"),
		p("creation.words.1000000", "creation", "preset.creation.words", 3, "legendary", "fa-gem", 300, "creator.published_words", 1000000, "百万字作者", "Million Words", "已发布章节累计 100 万字", "Publish 1,000,000 characters in total"),
		p("creation.likes.10", "creation", "preset.creation.likes", 1, "rare", "fa-heart", 30, "creator.likes_received", 10, "小有人气", "Getting Noticed", "作品累计获得 10 个点赞", "Get 10 likes on your books"),
		p("creation.likes.100", "creation", "preset.creation.likes", 2, "epic", "fa-fire", 100, "creator.likes_received", 100, "广受欢迎", "Crowd Favorite", "作品累计获得 100 个点赞", "Get 100 likes on your books"),
		p("creation.comments.10", "creation", "", 1, "rare", "fa-comments", 30, "creator.comments_received", 10, "引发讨论", "Conversation Starter", "作品累计收到 10 条评论", "Receive 10 comments on your work"),
		p("creation.views.1000", "creation", "preset.creation.views", 1, "rare", "fa-eye", 30, "creator.views_received", 1000, "千次浏览", "1K Views", "作品累计获得 1000 次浏览", "Get 1,000 views on your books"),
		p("creation.views.10000", "creation", "preset.creation.views", 2, "epic", "fa-mountain-sun", 100, "creator.views_received", 10000, "万众瞩目", "10K Views", "作品累计获得 1 万次浏览", "Get 10,000 views on your books"),
		// —— 互动 ——
		p("community.comments.1", "community", "preset.community.comments", 1, "common", "fa-comment", 5, "social.comments_created", 1, "初次发言", "First Words", "发表第一条评论", "Post your first comment"),
		p("community.comments.50", "community", "preset.community.comments", 2, "rare", "fa-bullhorn", 40, "social.comments_created", 50, "热心评论者", "Active Commenter", "累计发表 50 条评论", "Post 50 comments"),
		p("community.likes.10", "community", "", 1, "common", "fa-thumbs-up", 10, "social.likes_given", 10, "点赞达人", "Generous Liker", "为 10 本书点赞", "Like 10 books"),
		p("community.favorites.10", "community", "", 1, "common", "fa-bookmark", 10, "social.favorites_given", 10, "收藏家", "Collector", "收藏 10 本书", "Add 10 books to favorites"),
		// —— 账号 ——
		p("account.email_verified", "account", "", 1, "common", "fa-envelope-circle-check", 5, "account.email_verified", 1, "身份确认", "Verified", "完成邮箱验证", "Verify your email address"),
		p("account.two_factor", "account", "", 1, "rare", "fa-shield-halved", 20, "account.two_factor_enabled", 1, "安全卫士", "Security Guardian", "开启二次认证", "Turn on two-factor authentication"),
		p("account.oauth", "account", "", 1, "common", "fa-link", 5, "account.oauth_bindings", 1, "多端互联", "Connected", "绑定一个第三方账号", "Link a third-party account"),
		p("account.anniversary", "account", "", 1, "rare", "fa-cake-candles", 50, "account.age_days", 365, "一周年", "One Year Here", "注册满一年", "Be a member for one year"),
		p("account.invites.1", "account", "preset.account.invites", 1, "rare", "fa-user-plus", 30, "account.invited_users", 1, "引荐人", "Recruiter", "邀请 1 位用户注册", "Invite 1 user to sign up"),
		p("account.invites.10", "account", "preset.account.invites", 2, "epic", "fa-people-group", 150, "account.invited_users", 10, "社区大使", "Community Ambassador", "邀请 10 位用户注册", "Invite 10 users to sign up"),
		// —— 签到（需启用成长插件）——
		p("checkin.total.1", "account", "preset.checkin.total", 1, "common", "fa-calendar-check", 5, "checkin.total_days", 1, "初次签到", "First Check-in", "完成第一次每日签到", "Check in for the first time"),
		p("checkin.total.30", "account", "preset.checkin.total", 2, "rare", "fa-calendar-days", 30, "checkin.total_days", 30, "签到满月", "30 Check-ins", "累计签到 30 天", "Check in on 30 days"),
		p("checkin.total.100", "account", "preset.checkin.total", 3, "epic", "fa-star", 100, "checkin.total_days", 100, "百日签到", "100 Check-ins", "累计签到 100 天", "Check in on 100 days"),
		p("checkin.total.365", "account", "preset.checkin.total", 4, "legendary", "fa-sun", 300, "checkin.total_days", 365, "全年无休", "365 Check-ins", "累计签到 365 天", "Check in on 365 days"),
		p("checkin.streak.7", "account", "preset.checkin.streak", 1, "common", "fa-fire", 20, "checkin.longest_streak", 7, "连续一周", "7-Day Streak", "连续签到 7 天", "Check in 7 days in a row"),
		p("checkin.streak.30", "account", "preset.checkin.streak", 2, "epic", "fa-bolt", 100, "checkin.longest_streak", 30, "连续一月", "30-Day Streak", "连续签到 30 天", "Check in 30 days in a row"),
		p("checkin.streak.100", "account", "preset.checkin.streak", 3, "legendary", "fa-crown", 300, "checkin.longest_streak", 100, "百日不辍", "100-Day Streak", "连续签到 100 天", "Check in 100 days in a row"),
		// —— 成长（需启用成长插件）——
		p("growth.level.5", "account", "preset.growth.level", 1, "rare", "fa-star", 0, "growth.current_level", 5, "小有所成", "Rising Star", "成长等级达到 Lv.5", "Reach growth level 5"),
		p("growth.level.10", "account", "preset.growth.level", 2, "legendary", "fa-crown", 0, "growth.current_level", 10, "登峰造极", "Top Tier", "成长等级达到 Lv.10", "Reach growth level 10"),
		// —— 会员（指标由会员插件登记，会员插件启用时才可安装）——
		p("membership.days.1", "account", "preset.membership.days", 1, "rare", "fa-crown", 20, "membership.total_days", 1, "尊享之始", "Welcome Aboard", "首次成为会员", "Become a member for the first time"),
		p("membership.days.365", "account", "preset.membership.days", 2, "epic", "fa-gem", 100, "membership.total_days", 365, "年度会员", "Member of the Year", "累计成为会员 365 天", "Be a member for 365 days in total"),
		p("membership.days.1000", "account", "preset.membership.days", 3, "legendary", "fa-chess-king", 300, "membership.total_days", 1000, "千日相伴", "A Thousand Days", "累计成为会员 1000 天", "Be a member for 1,000 days in total"),
		p("membership.purchases.3", "account", "preset.membership.purchases", 1, "rare", "fa-heart", 50, "membership.purchases", 3, "忠实支持", "Loyal Supporter", "累计购买或续费会员 3 次", "Buy or renew a membership 3 times"),
	}
	for i := range list {
		list[i].SortOrder = (i + 1) * 10
	}
	return list
}()

// presetRequest 把预设转换为与后台创建一致的请求（中英文翻译均发布；中文名称作为兼容缓存）。
func presetRequest(preset presetAchievement, defaultLocale string) achievementDefinitionRequest {
	name, desc := preset.Name.ZH, preset.Desc.ZH
	if strings.HasPrefix(defaultLocale, "en") {
		name, desc = preset.Name.EN, preset.Desc.EN
	}
	req := achievementDefinitionRequest{
		Key: preset.Key, Name: name, NameEn: preset.Name.EN, Description: desc, DescriptionEn: preset.Desc.EN,
		LockedHint: desc, LockedHintEn: preset.Desc.EN,
		Category: preset.Category, Status: "active", Rarity: preset.Rarity, IconType: "fa", IconValue: preset.Icon,
		SeriesKey: preset.Series, Tier: preset.Tier, RewardXP: preset.RewardXP, SupersedesPrevious: preset.Series != "" && preset.Tier > 1,
		RuleLogic: "all", GrantMode: "auto", Visibility: "public", ProgressMode: "aggregate", SortOrder: preset.SortOrder,
		Rules: []achievementRuleRequest{{MetricKey: preset.Metric, Operator: "gte", TargetValue: preset.Target, WindowType: "lifetime"}},
	}
	req.Translations = map[string]plugincore.ResourceTranslation{
		"zh-CN": {Fields: map[string]string{"name": preset.Name.ZH, "description": preset.Desc.ZH, "locked_hint": preset.Desc.ZH}, Publish: true},
		"en":    {Fields: map[string]string{"name": preset.Name.EN, "description": preset.Desc.EN, "locked_hint": preset.Desc.EN}, Publish: true},
	}
	return req
}

// installPresets 安装指定 key 的预设（keys 为空表示全部）；已存在同 key 的成就跳过。返回新安装的数量。
func (am *behavior) installPresets(keys []string, actorID uint) (int, error) {
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	defaultLocale, err := am.core.DefaultContentLocale()
	if err != nil {
		defaultLocale = "zh-CN"
	}
	db := am.core.Gorm()
	var existing []string
	db.Model(&models.AchievementDefinition{}).Pluck("achievement_key", &existing)
	have := make(map[string]bool, len(existing))
	for _, k := range existing {
		have[k] = true
	}
	installed := 0
	for _, preset := range presetAchievements {
		if have[preset.Key] || (len(want) > 0 && !want[preset.Key]) || !am.metricAvailable(preset.Metric) {
			continue
		}
		req := presetRequest(preset, defaultLocale)
		if err := normalizeAchievementRequest(&req); err != nil {
			return installed, fmt.Errorf("预设 %s 无效: %w", preset.Key, err)
		}
		definition := definitionFromRequest(req, actorID)
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&definition).Error; err != nil {
				return err
			}
			// 逐语言保存翻译：站点停用了某内容语言时跳过该语言，不影响安装
			for locale, tr := range req.Translations {
				_ = am.core.SaveResourceTranslations(tx, resourceKind, definition.ID, actorID, map[string]plugincore.ResourceTranslation{locale: tr})
			}
			rules, err := rulesFromRequest(definition.ID, req.Rules)
			if err != nil {
				return err
			}
			if err := tx.Create(&rules).Error; err != nil {
				return err
			}
			return am.saveAchievementDefinitionVersion(tx, definition, rules, actorID)
		})
		if err != nil {
			return installed, fmt.Errorf("安装预设 %s 失败: %w", preset.Key, err)
		}
		installed++
	}
	return installed, nil
}

func init() {
	// 插件首次启用且还没有任何成就定义时，自动安装全部预设（开箱即有可玩内容）；已有成就的站点不自动添加。
	plugincore.OnPluginEnabled(plugins.KeyAchievements, func(core plugincore.Core) error {
		b := &behavior{core: core}
		var count int64
		core.Gorm().Model(&models.AchievementDefinition{}).Count(&count)
		if count > 0 {
			return nil
		}
		_, err := b.installPresets(nil, 0)
		return err
	})
}

type presetItem struct {
	Key       string            `json:"key"`
	Category  string            `json:"category"`
	Series    string            `json:"series_key"`
	Tier      int               `json:"tier"`
	Rarity    string            `json:"rarity"`
	Icon      string            `json:"icon_value"`
	RewardXP  int               `json:"reward_xp"`
	Metric    string            `json:"metric_key"`
	Target    int64             `json:"target_value"`
	Name      map[string]string `json:"name"`
	Desc      map[string]string `json:"description"`
	Installed bool              `json:"installed"`
}

// AdminListPresets GET /admin/achievement-presets 全部预设及是否已安装（同 key 成就已存在即视为已安装）。
func (am *behavior) AdminListPresets(c *gin.Context) {
	var existing []string
	am.core.Gorm().Model(&models.AchievementDefinition{}).Pluck("achievement_key", &existing)
	have := make(map[string]bool, len(existing))
	for _, k := range existing {
		have[k] = true
	}
	items := make([]presetItem, 0, len(presetAchievements))
	for _, preset := range presetAchievements {
		if !am.metricAvailable(preset.Metric) && !have[preset.Key] {
			continue // 所属插件未启用的预设不列出（如会员）
		}
		items = append(items, presetItem{
			Key: preset.Key, Category: preset.Category, Series: preset.Series, Tier: preset.Tier, Rarity: preset.Rarity,
			Icon: preset.Icon, RewardXP: preset.RewardXP, Metric: preset.Metric, Target: preset.Target,
			Name:      map[string]string{"zh-CN": preset.Name.ZH, "en": preset.Name.EN},
			Desc:      map[string]string{"zh-CN": preset.Desc.ZH, "en": preset.Desc.EN},
			Installed: have[preset.Key],
		})
	}
	am.core.OK(c, gin.H{"items": items})
}

// AdminInstallPresets POST /admin/achievement-presets/install {keys?: []} 安装选中的（缺省为全部）未安装预设，并触发重算。
func (am *behavior) AdminInstallPresets(c *gin.Context) {
	var req struct {
		Keys []string `json:"keys"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		am.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	installed, err := am.installPresets(req.Keys, am.core.CurrentUser(c).ID)
	if err != nil {
		am.core.Fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if installed > 0 {
		am.core.RecordAudit(c, "achievement.presets_installed", "achievement", "presets", "预设成就", map[string]any{"installed": installed})
		if am.achievementSettings().Enabled {
			_, _ = am.enqueueAchievementRecalculation(0)
		}
	}
	am.core.OK(c, gin.H{"installed": installed})
}
