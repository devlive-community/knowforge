package achievements_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"knowforge/server/internal/app"
	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins/achievements"
	"knowforge/server/internal/testdb"
)

// 集成测试：启动完整应用（安装向导），经插件管理接口启用成就插件，走真实路由/中间件/任务队列。

type testEnv struct {
	app    *app.App
	db     *gorm.DB
	admin  *models.User
	token  string
	server *httptest.Server
	client *http.Client
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	a, err := app.New(cfg)
	if err != nil {
		t.Fatalf("创建应用失败: %v", err)
	}
	e := &testEnv{app: a, server: httptest.NewServer(a.Router()), client: &http.Client{Timeout: 10 * time.Second}}
	t.Cleanup(e.server.Close)
	status, installed := e.do(t, http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"成就测试"},"admin":{"username":"achievement-admin","email":"achievement-admin@test.local","password":"secret123"}}`, "")
	if status != http.StatusOK {
		t.Fatalf("安装失败: %d %v", status, installed)
	}
	e.token = installed["data"].(map[string]any)["token"].(string)
	e.db = a.DB
	e.admin = &models.User{}
	if err := e.db.Where("username = ?", "achievement-admin").First(e.admin).Error; err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *testEnv) do(t *testing.T, method, path, body, token string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, e.server.URL+path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	payload := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&payload)
	return resp.StatusCode, payload
}

// enable 经插件管理接口启用插件（建表 + 权限 + 启用钩子），并清空启用时投递的任务（如成就全量重算）。
func (e *testEnv) enable(t *testing.T, key string) {
	t.Helper()
	if status, payload := e.do(t, http.MethodPost, "/api/v1/admin/plugins/"+key+"/install", "", e.token); status != http.StatusOK {
		t.Fatalf("启用插件 %s 失败: %d %v", key, status, payload)
	}
	e.drainJobs(t)
}

func (e *testEnv) drainJobs(t *testing.T) {
	t.Helper()
	for i := 0; i < 20; i++ {
		ran, err := e.app.Jobs.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("执行后台任务失败: %v", err)
		}
		if !ran {
			return
		}
	}
}

func (e *testEnv) user(t *testing.T, username string) *models.User {
	t.Helper()
	u := &models.User{Username: username, Email: username + "@test.local", Role: "user", IsActive: true}
	if err := e.db.Create(u).Error; err != nil {
		t.Fatal(err)
	}
	return u
}

func (e *testEnv) tokenFor(t *testing.T, u *models.User) string {
	t.Helper()
	token, err := auth.GenerateToken(e.app.Config.Secret, u.ID, u.Username, u.Role)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestAchievementModuleDefaultsToDisabled(t *testing.T) {
	e := newTestEnv(t)
	settings := achievements.LoadSettings(e.app)
	if settings.Enabled {
		t.Fatal("achievement module must be disabled by default")
	}
	if !settings.PublicProfileEnabled || !settings.NotificationsEnabled || !settings.AllowUserHide || settings.ShowcaseLimit != 6 {
		t.Fatalf("unexpected achievement defaults: %+v", settings)
	}
}

func TestAchievementEvaluationCreatesProgressAndSingleGrant(t *testing.T) {
	e := newTestEnv(t)
	e.enable(t, "achievements")
	_ = e.app.SetSetting("achievements_notifications_enabled", "false", "test")
	user := e.user(t, "achievement-reader")
	book := models.Book{Title: "Achievement Book", Slug: "achievement-book", UserID: user.ID, Status: "published", IsPublic: true}
	if err := e.db.Create(&book).Error; err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		doc := models.Document{BookID: book.ID, UserID: user.ID, Title: "Chapter", Slug: "achievement-chapter-" + string(rune('a'+index)), Status: "published"}
		if err := e.db.Create(&doc).Error; err != nil {
			t.Fatal(err)
		}
		if err := e.db.Create(&models.ReadChapter{UserID: user.ID, BookID: book.ID, DocID: doc.ID}).Error; err != nil {
			t.Fatal(err)
		}
	}
	definition := models.AchievementDefinition{Key: "reading.three", Name: "读完三章", Category: "reading", Status: "active", Rarity: "common", IconType: "fa", IconValue: "fa-book", RuleLogic: "all", GrantMode: "auto", Visibility: "public", ProgressMode: "aggregate", Version: 1, Tier: 1, CreatedBy: user.ID, UpdatedBy: user.ID}
	if err := e.db.Create(&definition).Error; err != nil {
		t.Fatal(err)
	}
	rule := models.AchievementRule{AchievementID: definition.ID, MetricKey: "reading.chapters_read", Operator: "gte", TargetValue: 3, WindowType: "lifetime", Filters: "{}"}
	if err := e.db.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	definition.Rules = []models.AchievementRule{rule}
	for i := 0; i < 2; i++ {
		if err := achievements.EvaluateForUser(e.app, user.ID, definition); err != nil {
			t.Fatal(err)
		}
	}
	var progress models.UserAchievementProgress
	if err := e.db.Where("user_id = ? AND achievement_id = ?", user.ID, definition.ID).First(&progress).Error; err != nil {
		t.Fatal(err)
	}
	if progress.Percent != 100 || progress.Status != "unlocked" || progress.CurrentValue != 3 {
		t.Fatalf("unexpected progress: %+v", progress)
	}
	// 回归：时间字段不能为零值（MySQL 严格模式拒绝 '0000-00-00'）
	if progress.LastEvaluatedAt.IsZero() {
		t.Fatal("last_evaluated_at 不应为零值")
	}
	var grants int64
	e.db.Model(&models.UserAchievement{}).Where("user_id = ? AND achievement_id = ?", user.ID, definition.ID).Count(&grants)
	if grants != 1 {
		t.Fatalf("achievement must be granted once, got %d", grants)
	}
}

// 核心发出的业务活动 → 插件幂等地记事件并投递评估任务 → 任务执行后授予成就。
func TestAchievementEventIsDeduplicatedAndProcessed(t *testing.T) {
	e := newTestEnv(t)
	e.enable(t, "achievements")
	_ = e.app.SetSetting("achievements_notifications_enabled", "false", "test")
	user := e.user(t, "achievement-verified")
	definition := models.AchievementDefinition{Key: "account.first-day", Name: "加入一天", Category: "account", Status: "active", Rarity: "common", IconType: "fa", IconValue: "fa-user", RuleLogic: "all", GrantMode: "auto", Visibility: "public", ProgressMode: "aggregate", Version: 1, Tier: 1, CreatedBy: user.ID, UpdatedBy: user.ID}
	if err := e.db.Create(&definition).Error; err != nil {
		t.Fatal(err)
	}
	rule := models.AchievementRule{AchievementID: definition.ID, MetricKey: "account.email_verified", Operator: "gte", TargetValue: 1, WindowType: "lifetime", Filters: "{}"}
	if err := e.db.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.db.Model(user).Update("email_verified", true).Error; err != nil {
		t.Fatal(err)
	}
	ev := plugincore.ActivityEvent{UserID: user.ID, Type: "account.updated", SourceType: "user", SourceID: "1", DedupeKey: "account.updated:test"}
	plugincore.FireActivity(e.app, ev)
	plugincore.FireActivity(e.app, ev)
	var events, jobs int64
	e.db.Model(&models.AchievementEvent{}).Count(&events)
	e.db.Model(&models.BackgroundJob{}).Where("type = ? AND status = ?", achievements.EvaluateJobType, "pending").Count(&jobs)
	if events != 1 || jobs != 1 {
		t.Fatalf("event and job must be deduplicated: events=%d jobs=%d", events, jobs)
	}
	if ran, err := e.app.Jobs.RunOnce(context.Background()); !ran || err != nil {
		t.Fatalf("achievement event job failed: ran=%v err=%v", ran, err)
	}
	var event models.AchievementEvent
	if err := e.db.First(&event).Error; err != nil || event.ProcessedAt == nil {
		t.Fatalf("achievement event was not marked processed: %+v err=%v", event, err)
	}
	var grants int64
	e.db.Model(&models.UserAchievement{}).Where("user_id = ? AND achievement_id = ?", user.ID, definition.ID).Count(&grants)
	if grants != 1 {
		t.Fatalf("event must unlock achievement once, got %d", grants)
	}
}

func TestAchievementAdminPermissionAndPublicPayload(t *testing.T) {
	e := newTestEnv(t)
	regular := e.user(t, "achievement-regular")
	userToken := e.tokenFor(t, regular)
	// 插件未启用：管理接口挂启用守卫 → 404
	if status, _ := e.do(t, http.MethodGet, "/api/v1/admin/achievement-metrics", "", e.token); status != http.StatusNotFound {
		t.Fatalf("disabled plugin must hide achievement management, got %d", status)
	}
	e.enable(t, "achievements")
	if status, _ := e.do(t, http.MethodGet, "/api/v1/admin/achievement-metrics", "", ""); status != http.StatusUnauthorized {
		t.Fatalf("anonymous achievement management must be 401, got %d", status)
	}
	if status, _ := e.do(t, http.MethodGet, "/api/v1/admin/achievement-metrics", "", userToken); status != http.StatusForbidden {
		t.Fatalf("regular user achievement management must be 403, got %d", status)
	}
	if status, payload := e.do(t, http.MethodGet, "/api/v1/admin/achievement-metrics", "", e.token); status != http.StatusOK || payload["data"] == nil {
		t.Fatalf("admin achievement management failed: %d %v", status, payload)
	}

	definition := models.AchievementDefinition{Key: "public.safe", Name: "公开成就", Category: "special", Status: "archived", Rarity: "rare", IconType: "fa", IconValue: "fa-award", Visibility: "public", Tier: 1, CreatedBy: e.admin.ID, UpdatedBy: e.admin.ID}
	if err := e.db.Create(&definition).Error; err != nil {
		t.Fatal(err)
	}
	grant := models.UserAchievement{UserID: regular.ID, AchievementID: definition.ID, DefinitionVersion: 1, Source: "manual", GrantorID: e.admin.ID, Reason: "private admin reason", MetricsSnapshot: `{"secret":true}`, IsPublic: true, UnlockedAt: time.Now()}
	if err := e.db.Create(&grant).Error; err != nil {
		t.Fatal(err)
	}
	status, payload := e.do(t, http.MethodGet, "/api/v1/users/"+regular.Username+"/achievements", "", "")
	if status != http.StatusOK {
		t.Fatalf("public achievements failed: %d %v", status, payload)
	}
	raw, _ := json.Marshal(payload)
	if strings.Contains(string(raw), "private admin reason") || strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "grantor_id") {
		t.Fatalf("public achievement payload leaked internal grant data: %s", raw)
	}
	// 我的成就（用户端）
	if status, payload := e.do(t, http.MethodGet, "/api/v1/users/me/achievements", "", userToken); status != http.StatusOK {
		t.Fatalf("my achievements failed: %d %v", status, payload)
	}
	// 多语言资源接口保持原路径
	if status, payload := e.do(t, http.MethodGet, "/api/v1/admin/i18n/resources/achievement/"+jsonID(definition.ID), "", e.token); status != http.StatusOK {
		t.Fatalf("achievement translations endpoint failed: %d %v", status, payload)
	}
	// 删除用户时插件登记的成就数据一并清理
	if status, payload := e.do(t, http.MethodDelete, "/api/v1/admin/users/"+jsonID(regular.ID), "", e.token); status != http.StatusOK {
		t.Fatalf("delete user failed: %d %v", status, payload)
	}
	var left int64
	e.db.Model(&models.UserAchievement{}).Where("user_id = ?", regular.ID).Count(&left)
	if left != 0 {
		t.Fatalf("deleting a user must remove their achievements, left %d", left)
	}
}

// 成就指标：growth.total_xp / growth.current_level / reading.books_completed / creator.published_words。
func TestAchievementMetricsAcrossPlugins(t *testing.T) {
	e := newTestEnv(t)
	e.enable(t, "achievements")
	e.enable(t, "growth")
	user := e.user(t, "achievement-metrics")

	// 成长：150 经验 → Lv.2
	plugincore.RecordExperience(e.app, user.ID, "test", "x", "1", "d1", 150, "")
	metric := func(key string) int64 {
		v, err := achievements.EvaluateMetric(e.app, user.ID, models.AchievementRule{MetricKey: key, WindowType: "lifetime"})
		if err != nil {
			t.Fatalf("metric %s: %v", key, err)
		}
		return v
	}
	if v := metric("growth.total_xp"); v != 150 {
		t.Fatalf("growth.total_xp 应为 150，实际 %d", v)
	}
	if v := metric("growth.current_level"); v != 2 {
		t.Fatalf("growth.current_level 应为 2，实际 %d", v)
	}

	book := models.Book{Title: "B", Slug: "words-book", UserID: user.ID, Status: "published", IsPublic: true}
	e.db.Create(&book)
	e.db.Create(&models.Document{BookID: book.ID, UserID: user.ID, Title: "c1", Slug: "c1", Status: "published", Content: "12345"})
	e.db.Create(&models.Document{BookID: book.ID, UserID: user.ID, Title: "c2", Slug: "c2", Status: "published", Content: "abc"})
	if v := metric("creator.published_words"); v != 8 {
		t.Fatalf("creator.published_words 应为 8，实际 %d", v)
	}
	var docs []models.Document
	e.db.Where("book_id = ?", book.ID).Find(&docs)
	for _, d := range docs {
		e.db.Create(&models.ReadChapter{UserID: user.ID, BookID: book.ID, DocID: d.ID})
	}
	if v := metric("reading.books_completed"); v != 1 {
		t.Fatalf("reading.books_completed 应为 1，实际 %d", v)
	}
}

func jsonID(id uint) string {
	raw, _ := json.Marshal(id)
	return string(raw)
}

// 撤销成就收回奖励经验；重新授予再次发放；站点配置由插件下发 achievements_enabled。
func TestAchievementRevokeReclaimsRewardXP(t *testing.T) {
	e := newTestEnv(t)
	e.enable(t, "achievements")
	e.enable(t, "growth")
	_ = e.app.SetSetting("achievements_notifications_enabled", "false", "test")
	_, site := e.do(t, http.MethodGet, "/api/v1/site", "", "")
	if site["data"].(map[string]any)["achievements_enabled"] != "true" {
		t.Fatalf("启用后站点配置应含 achievements_enabled=true: %v", site["data"])
	}
	user := e.user(t, "achievement-reward")
	definition := models.AchievementDefinition{Key: "reward.xp", Name: "奖励成就", Category: "special", Status: "active", Rarity: "rare", IconType: "fa", IconValue: "fa-award", Visibility: "public", GrantMode: "manual", RewardXP: 40, Version: 1, Tier: 1, CreatedBy: e.admin.ID, UpdatedBy: e.admin.ID}
	if err := e.db.Create(&definition).Error; err != nil {
		t.Fatal(err)
	}
	xp := func() int64 {
		var total int64
		e.db.Model(&models.ExperienceEvent{}).Where("user_id = ?", user.ID).Select("COALESCE(SUM(final_xp),0)").Scan(&total)
		return total
	}
	grantBody := `{"username":"achievement-reward","achievement_id":` + jsonID(definition.ID) + `,"reason":"活动"}`
	status, payload := e.do(t, http.MethodPost, "/api/v1/admin/achievement-grants", grantBody, e.token)
	if status != http.StatusOK || xp() != 40 {
		t.Fatalf("授予成就应奖励 40 经验: %d %v xp=%d", status, payload, xp())
	}
	grantID := jsonID(uint(payload["data"].(map[string]any)["id"].(float64)))
	if status, payload := e.do(t, http.MethodPost, "/api/v1/admin/achievement-grants/"+grantID+"/revoke", `{"reason":"误发"}`, e.token); status != http.StatusOK || xp() != 0 {
		t.Fatalf("撤销成就应收回奖励经验: %d %v xp=%d", status, payload, xp())
	}
	if status, _ := e.do(t, http.MethodPost, "/api/v1/admin/achievement-grants", grantBody, e.token); status != http.StatusOK || xp() != 40 {
		t.Fatalf("重新授予应再次奖励经验，实际 xp=%d", xp())
	}
	e.do(t, http.MethodPost, "/api/v1/admin/achievement-grants/"+grantID+"/revoke", `{"reason":"再次撤销"}`, e.token)
	if xp() != 0 {
		t.Fatalf("再次撤销应收回重新发放的经验，实际 xp=%d", xp())
	}
}

// 奖励经验「只一份」：成长插件停用期间撤销/重新授予，重新启用后对账——撤销的收回、有效的保持一份，绝不重复；
// 停用期间获得的成就在启用后补发一份。
func TestAchievementRewardXPExactlyOnceAcrossGrowthToggles(t *testing.T) {
	e := newTestEnv(t)
	e.enable(t, "achievements")
	e.enable(t, "growth")
	_ = e.app.SetSetting("achievements_notifications_enabled", "false", "test")
	user := e.user(t, "reward-once")
	other := e.user(t, "reward-backfill")
	definition := models.AchievementDefinition{Key: "reward.once", Name: "唯一奖励", Category: "special", Status: "active", Rarity: "rare", IconType: "fa", IconValue: "fa-award", Visibility: "public", GrantMode: "manual", RewardXP: 40, Version: 1, Tier: 1, CreatedBy: e.admin.ID, UpdatedBy: e.admin.ID}
	if err := e.db.Create(&definition).Error; err != nil {
		t.Fatal(err)
	}
	xp := func(u *models.User) int64 {
		var total int64
		e.db.Model(&models.ExperienceEvent{}).Where("user_id = ?", u.ID).Select("COALESCE(SUM(final_xp),0)").Scan(&total)
		return total
	}
	grant := func(u *models.User) string {
		status, payload := e.do(t, http.MethodPost, "/api/v1/admin/achievement-grants", `{"username":"`+u.Username+`","achievement_id":`+jsonID(definition.ID)+`,"reason":"r"}`, e.token)
		if status != http.StatusOK {
			t.Fatalf("授予失败: %d %v", status, payload)
		}
		return jsonID(uint(payload["data"].(map[string]any)["id"].(float64)))
	}
	revoke := func(id string) {
		if status, payload := e.do(t, http.MethodPost, "/api/v1/admin/achievement-grants/"+id+"/revoke", `{"reason":"r"}`, e.token); status != http.StatusOK {
			t.Fatalf("撤销失败: %d %v", status, payload)
		}
	}
	setGrowth := func(on bool) {
		action := "uninstall"
		if on {
			action = "install"
		}
		if status, _ := e.do(t, http.MethodPost, "/api/v1/admin/plugins/growth/"+action, "", e.token); status != http.StatusOK {
			t.Fatalf("%s growth 失败", action)
		}
		e.drainJobs(t) // 执行启用时投递的奖励对账任务
	}

	id := grant(user)
	if xp(user) != 40 {
		t.Fatalf("授予应得 40，实际 %d", xp(user))
	}
	// 停用成长 → 撤销（此时无法收回）→ 重新启用：对账收回
	setGrowth(false)
	revoke(id)
	setGrowth(true)
	if xp(user) != 0 {
		t.Fatalf("停用期间撤销的成就，重新启用后应收回奖励，实际 %d", xp(user))
	}
	// 停用成长 → 重新授予 → 启用：补发一份
	setGrowth(false)
	grant(user)
	setGrowth(true)
	if xp(user) != 40 {
		t.Fatalf("重新启用后应只持有一份奖励，实际 %d", xp(user))
	}
	// 停用成长 → 撤销 + 重新授予（有效）→ 启用：仍只一份，不重复
	setGrowth(false)
	revoke(id)
	grant(user)
	setGrowth(true)
	if xp(user) != 40 {
		t.Fatalf("反复撤销/授予后仍只能持有一份奖励，实际 %d", xp(user))
	}
	// 启用状态下多次对账也不重复
	setGrowth(false)
	setGrowth(true)
	if xp(user) != 40 {
		t.Fatalf("重复对账不应重复发放，实际 %d", xp(user))
	}
	// 停用期间获得的成就，启用后补发
	setGrowth(false)
	grant(other)
	setGrowth(true)
	if xp(other) != 40 {
		t.Fatalf("停用期间获得的成就应在启用后补发一份，实际 %d", xp(other))
	}
}

// 签到成就指标：累计签到天数（可按窗口）、当前连续（中断归零）、最长连续。
func TestCheckinAchievementMetrics(t *testing.T) {
	e := newTestEnv(t)
	e.enable(t, "achievements")
	e.enable(t, "growth")
	user := e.user(t, "checkin-metrics")
	day := func(offset int) string { return time.Now().AddDate(0, 0, offset).Format("2006-01-02") }
	// 早期一段连续 5 天（已中断），最近连续 3 天到昨天
	for i, off := range []int{-20, -19, -18, -17, -16} {
		e.db.Create(&models.UserCheckin{UserID: user.ID, Day: day(off), Streak: i + 1})
	}
	for i, off := range []int{-3, -2, -1} {
		e.db.Create(&models.UserCheckin{UserID: user.ID, Day: day(off), Streak: i + 1})
	}
	metric := func(key, window string, value int) int64 {
		v, err := achievements.EvaluateMetric(e.app, user.ID, models.AchievementRule{MetricKey: key, WindowType: window, WindowValue: value})
		if err != nil {
			t.Fatalf("metric %s: %v", key, err)
		}
		return v
	}
	if v := metric("checkin.total_days", "lifetime", 0); v != 8 {
		t.Fatalf("累计签到应为 8，实际 %d", v)
	}
	if v := metric("checkin.longest_streak", "lifetime", 0); v != 5 {
		t.Fatalf("最长连续应为 5，实际 %d", v)
	}
	if v := metric("checkin.current_streak", "lifetime", 0); v != 3 {
		t.Fatalf("当前连续（到昨天）应为 3，实际 %d", v)
	}
	// 断签：最近一次签到在 2 天前 → 当前连续归零
	e.db.Where("user_id = ? AND day = ?", user.ID, day(-1)).Delete(&models.UserCheckin{})
	if v := metric("checkin.current_streak", "lifetime", 0); v != 0 {
		t.Fatalf("断签后当前连续应为 0，实际 %d", v)
	}
}

// 预设成就：首次启用自动安装全部预设（均为启用中的自动成就、带中英文翻译与版本快照）；读完一章即解锁「开卷有益」；
// 管理员可查看安装状态、按需补装被删除的预设；重复启用不会重复安装。
func TestPresetAchievements(t *testing.T) {
	e := newTestEnv(t)
	e.enable(t, "achievements")
	_ = e.app.SetSetting("achievements_notifications_enabled", "false", "test")

	var defs []models.AchievementDefinition
	e.db.Preload("Rules").Where("achievement_key LIKE ?", "preset.%").Find(&defs)
	if len(defs) != achievements.PresetCount() {
		t.Fatalf("首次启用应安装全部 %d 个预设，实际 %d", achievements.PresetCount(), len(defs))
	}
	for _, d := range defs {
		if d.Status != "active" || d.GrantMode != "auto" || len(d.Rules) != 1 {
			t.Fatalf("预设 %s 应为启用中的自动成就且有 1 条规则: %+v", d.Key, d)
		}
		var locales int64
		e.db.Model(&models.LocalizedResourceContent{}).Where("resource_type = ? AND resource_id = ? AND published <> ''", "achievement", d.ID).Count(&locales)
		var versions int64
		e.db.Model(&models.AchievementDefinitionVersion{}).Where("achievement_id = ?", d.ID).Count(&versions)
		if locales != 2 || versions != 1 {
			t.Fatalf("预设 %s 应发布中英文翻译并有版本快照: locales=%d versions=%d", d.Key, locales, versions)
		}
	}

	// 端到端：读完一章 → 业务活动 → 评估任务 → 解锁「开卷有益」
	user := e.user(t, "preset-reader")
	book := models.Book{Title: "P", Slug: "preset-book", UserID: e.admin.ID, Status: "published", IsPublic: true}
	e.db.Create(&book)
	doc := models.Document{BookID: book.ID, UserID: e.admin.ID, Title: "c", Slug: "c", Status: "published"}
	e.db.Create(&doc)
	e.db.Create(&models.ReadChapter{UserID: user.ID, BookID: book.ID, DocID: doc.ID})
	plugincore.FireActivity(e.app, plugincore.ActivityEvent{UserID: user.ID, Type: "chapter.read", SourceType: "document", SourceID: jsonID(doc.ID), DedupeKey: "chapter.read:preset"})
	e.drainJobs(t)
	var first models.AchievementDefinition
	e.db.Where("achievement_key = ?", "preset.reading.chapters.1").First(&first)
	var grants int64
	e.db.Model(&models.UserAchievement{}).Where("user_id = ? AND achievement_id = ?", user.ID, first.ID).Count(&grants)
	if grants != 1 {
		t.Fatalf("读完第一章应解锁「开卷有益」，实际授予 %d", grants)
	}

	// 已有授予的预设被「删除」只会归档，仍算已安装（不会被重新装回，尊重管理员的处理）
	if status, payload := e.do(t, http.MethodDelete, "/api/v1/admin/achievements/"+jsonID(first.ID), "", e.token); status != http.StatusOK {
		t.Fatalf("删除预设失败: %d %v", status, payload)
	}
	// 模拟旧站点缺少某个（后来新增的）预设 → 列表显示未安装 → 按需补装；再次安装不重复
	var missing models.AchievementDefinition
	e.db.Where("achievement_key = ?", "preset.account.anniversary").First(&missing)
	e.db.Where("achievement_id = ?", missing.ID).Delete(&models.AchievementRule{})
	e.db.Where("achievement_id = ?", missing.ID).Delete(&models.AchievementDefinitionVersion{})
	e.db.Where("resource_type = ? AND resource_id = ?", "achievement", missing.ID).Delete(&models.LocalizedResourceContent{})
	e.db.Delete(&missing)
	_, payload := e.do(t, http.MethodGet, "/api/v1/admin/achievement-presets", "", e.token)
	notInstalled := []string{}
	for _, it := range payload["data"].(map[string]any)["items"].([]any) {
		if item := it.(map[string]any); item["installed"] == false {
			notInstalled = append(notInstalled, item["key"].(string))
		}
	}
	if len(notInstalled) != 1 || notInstalled[0] != "preset.account.anniversary" {
		t.Fatalf("删除后应只有该预设显示未安装: %v", notInstalled)
	}
	_, payload = e.do(t, http.MethodPost, "/api/v1/admin/achievement-presets/install", `{"keys":["preset.account.anniversary"]}`, e.token)
	if payload["data"].(map[string]any)["installed"].(float64) != 1 {
		t.Fatalf("应补装 1 个预设: %v", payload)
	}
	_, payload = e.do(t, http.MethodPost, "/api/v1/admin/achievement-presets/install", `{}`, e.token)
	if payload["data"].(map[string]any)["installed"].(float64) != 0 {
		t.Fatalf("全部已安装时不应重复安装: %v", payload)
	}

	// 停用再启用插件（已有成就）不会重复安装
	e.do(t, http.MethodPost, "/api/v1/admin/plugins/achievements/uninstall", "", e.token)
	e.enable(t, "achievements")
	var total int64
	e.db.Model(&models.AchievementDefinition{}).Where("achievement_key LIKE ?", "preset.%").Count(&total)
	if int(total) != achievements.PresetCount() {
		t.Fatalf("重复启用不应重复安装预设，实际 %d", total)
	}

	// 会员预设依赖会员插件登记的指标：启用会员后才列出，可补装
	e.enable(t, "membership")
	_, payload = e.do(t, http.MethodGet, "/api/v1/admin/achievement-presets", "", e.token)
	pending := 0
	for _, it := range payload["data"].(map[string]any)["items"].([]any) {
		if item := it.(map[string]any); item["installed"] == false {
			pending++
		}
	}
	if pending != achievements.AllPresetCount()-achievements.PresetCount() || pending == 0 {
		t.Fatalf("启用会员后应列出未安装的会员预设，实际 %d", pending)
	}
	_, payload = e.do(t, http.MethodPost, "/api/v1/admin/achievement-presets/install", `{}`, e.token)
	if int(payload["data"].(map[string]any)["installed"].(float64)) != pending {
		t.Fatalf("应安装全部会员预设: %v", payload)
	}
}

// 后台创建/更新成就时保存奖励经验（修复：此前请求结构缺少 reward_xp，表单填写的奖励经验不会生效），并校验范围。
func TestAchievementRewardXPIsSaved(t *testing.T) {
	e := newTestEnv(t)
	e.enable(t, "achievements")
	status, payload := e.do(t, http.MethodPost, "/api/v1/admin/achievements", `{"key":"custom.reward","name":"自定义奖励","category":"special","status":"draft","grant_mode":"manual","reward_xp":25}`, e.token)
	if status != http.StatusOK {
		t.Fatalf("创建成就失败: %d %v", status, payload)
	}
	var d models.AchievementDefinition
	e.db.Where("achievement_key = ?", "custom.reward").First(&d)
	if d.RewardXP != 25 {
		t.Fatalf("奖励经验应保存为 25，实际 %d", d.RewardXP)
	}
	if status, _ := e.do(t, http.MethodPut, "/api/v1/admin/achievements/"+jsonID(d.ID), `{"key":"custom.reward","name":"自定义奖励","category":"special","status":"draft","grant_mode":"manual","reward_xp":60}`, e.token); status != http.StatusOK {
		t.Fatalf("更新成就失败: %d", status)
	}
	e.db.First(&d, d.ID)
	if d.RewardXP != 60 {
		t.Fatalf("更新后奖励经验应为 60，实际 %d", d.RewardXP)
	}
	if status, _ := e.do(t, http.MethodPost, "/api/v1/admin/achievements", `{"key":"custom.too-much","name":"超额","category":"special","status":"draft","grant_mode":"manual","reward_xp":200000}`, e.token); status != http.StatusBadRequest {
		t.Fatalf("奖励经验超出上限应 400，实际 %d", status)
	}
}
