package growth_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"knowforge/server/internal/app"
	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins/growth"
	"knowforge/server/internal/testdb"
)

// 集成测试：启动完整应用，经插件管理接口启用/停用成长插件（建表、权限、种子等级与经验规则）。

type testEnv struct {
	app    *app.App
	db     *gorm.DB
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
	status, installed := e.do(t, http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"成长测试"},"admin":{"username":"growth-admin","email":"growth-admin@test.local","password":"secret123"}}`)
	if status != http.StatusOK {
		t.Fatalf("安装失败: %d %v", status, installed)
	}
	e.token = installed["data"].(map[string]any)["token"].(string)
	e.db = a.DB
	e.setPlugin(t, true)
	return e
}

func (e *testEnv) do(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, e.server.URL+path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if e.token != "" {
		req.Header.Set("Authorization", "Bearer "+e.token)
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

func (e *testEnv) setPlugin(t *testing.T, enabled bool) {
	t.Helper()
	action := "uninstall"
	if enabled {
		action = "install"
	}
	if status, payload := e.do(t, http.MethodPost, "/api/v1/admin/plugins/growth/"+action, ""); status != http.StatusOK {
		t.Fatalf("%s growth 插件失败: %d %v", action, status, payload)
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

// 成长等级：启用后种子等级，记账经验幂等、按阈值解析等级、升级写历史；禁用后不再结算。
func TestGrowthExperienceAndLeveling(t *testing.T) {
	e := newTestEnv(t)
	user := e.user(t, "growth-user")
	var levelCount int64
	e.db.Model(&models.LevelDefinition{}).Count(&levelCount)
	if levelCount == 0 {
		t.Fatal("启用后应种子默认等级")
	}

	plugincore.RecordExperience(e.app, user.ID, "test.grant", "x", "1", "dedupe-1", 150, "")
	if p := growth.Profile(e.app, user.ID); p.LifetimeXP != 150 || p.CurrentLevel != 2 {
		t.Fatalf("经验/等级错误：xp=%d level=%d", p.LifetimeXP, p.CurrentLevel)
	}
	plugincore.RecordExperience(e.app, user.ID, "test.grant", "x", "1", "dedupe-1", 150, "")
	if p := growth.Profile(e.app, user.ID); p.LifetimeXP != 150 {
		t.Fatalf("重复 dedupe 应不结算，实际 xp=%d", p.LifetimeXP)
	}
	plugincore.RecordExperience(e.app, user.ID, "test.grant", "x", "2", "dedupe-2", 100, "")
	p := growth.Profile(e.app, user.ID)
	if p.LifetimeXP != 250 || p.CurrentLevel != 3 || p.HighestLevel != 3 {
		t.Fatalf("升级错误：xp=%d level=%d highest=%d", p.LifetimeXP, p.CurrentLevel, p.HighestLevel)
	}
	var histCount int64
	e.db.Model(&models.UserLevelHistory{}).Where("user_id = ?", user.ID).Count(&histCount)
	if histCount < 2 {
		t.Fatalf("应有 ≥2 条升级历史，实际 %d", histCount)
	}

	e.setPlugin(t, false)
	plugincore.RecordExperience(e.app, user.ID, "test.grant", "x", "3", "dedupe-3", 100, "")
	if p := growth.Profile(e.app, user.ID); p.LifetimeXP != 250 {
		t.Fatalf("禁用后不应结算，实际 xp=%d", p.LifetimeXP)
	}
}

// 经验规则：按 base_xp 发放、执行每日上限、禁用规则不发。
func TestExperienceRuleAndDailyCap(t *testing.T) {
	e := newTestEnv(t)
	user := e.user(t, "growth-cap")
	e.db.Create(&models.ExperienceRule{RuleKey: "test.rule", Label: "T", BaseXP: 5, DailyCap: 10, Enabled: true})
	for i := 0; i < 4; i++ {
		growth.AwardExperience(e.app, user.ID, "test.rule", "x", "1", "cap-"+strconv.Itoa(i))
	}
	if p := growth.Profile(e.app, user.ID); p.LifetimeXP != 10 {
		t.Fatalf("每日上限应封顶在 10，实际 %d", p.LifetimeXP)
	}
	e.db.Model(&models.ExperienceRule{}).Where("rule_key = ?", "test.rule").Update("enabled", false)
	growth.AwardExperience(e.app, user.ID, "test.rule", "x", "1", "disabled-1")
	if p := growth.Profile(e.app, user.ID); p.LifetimeXP != 10 {
		t.Fatalf("禁用规则不应发经验，实际 %d", p.LifetimeXP)
	}
}

// 核心事件 → 经验：评论（3）、首次读章节（5）、章节发布（10），各自按去重键只结算一次。
func TestCoreEventsAwardExperience(t *testing.T) {
	e := newTestEnv(t)
	user := e.user(t, "growth-events")
	book := models.Book{Title: "G", Slug: "growth-book", UserID: user.ID, Status: "published", IsPublic: true}
	e.db.Create(&book)
	doc := models.Document{BookID: book.ID, UserID: user.ID, Title: "c1", Slug: "c1", Status: "published"}
	e.db.Create(&doc)
	docID := strconv.FormatUint(uint64(doc.ID), 10)

	comment := plugincore.ActivityEvent{UserID: user.ID, Type: "comment.created", SourceType: "comment", SourceID: "42", DedupeKey: "comment.given:42"}
	plugincore.FireActivity(e.app, comment)
	plugincore.FireActivity(e.app, comment)
	read := models.ReadChapter{UserID: user.ID, BookID: book.ID, DocID: doc.ID}
	e.db.Create(&read)
	plugincore.FireActivity(e.app, plugincore.ActivityEvent{UserID: user.ID, Type: "chapter.read", SourceType: "document", SourceID: docID, DedupeKey: "chapter.read:1"})
	plugincore.FireChapterPublished(e.app, &book, &doc)
	plugincore.FireChapterPublished(e.app, &book, &doc)

	if p := growth.Profile(e.app, user.ID); p.LifetimeXP != 3+5+10 {
		t.Fatalf("评论/读章/发布应共得 18 经验，实际 %d", p.LifetimeXP)
	}
	var keys []string
	e.db.Model(&models.ExperienceEvent{}).Where("user_id = ?", user.ID).Order("id ASC").Pluck("dedupe_key", &keys)
	want := []string{"community.comment:42", "reading.chapter:" + strconv.FormatUint(uint64(read.ID), 10), "creation.chapter_published:" + docID}
	if len(keys) != len(want) || keys[0] != want[0] || keys[1] != want[1] || keys[2] != want[2] {
		t.Fatalf("经验去重键应与迁移前一致: %v want %v", keys, want)
	}

	// 删除用户（无书籍）时一并清理其成长数据
	leaver := e.user(t, "growth-leaver")
	plugincore.RecordExperience(e.app, leaver.ID, "test.grant", "x", "1", "leaver-1", 120, "")
	if status, payload := e.do(t, http.MethodDelete, "/api/v1/admin/users/"+strconv.FormatUint(uint64(leaver.ID), 10), ""); status != http.StatusOK {
		t.Fatalf("delete user failed: %d %v", status, payload)
	}
	var left int64
	e.db.Model(&models.ExperienceEvent{}).Where("user_id = ?", leaver.ID).Count(&left)
	if left != 0 {
		t.Fatalf("删除用户后经验流水应被清理，剩余 %d", left)
	}
}

// 经验触发器目录：管理端查看规则时补建新增触发器（默认停用，原有 3 条保持启用）；
// 启用后对应业务活动发经验；经验流水可按用户/规则筛选；人工调整可按用户 ID。
func TestExperienceCatalogLedgerAndAdjust(t *testing.T) {
	e := newTestEnv(t)
	user := e.user(t, "growth-catalog")

	status, payload := e.do(t, http.MethodGet, "/api/v1/admin/growth/rules", "")
	if status != http.StatusOK {
		t.Fatalf("读取经验规则失败: %d %v", status, payload)
	}
	rules := map[string]map[string]any{}
	for _, it := range payload["data"].(map[string]any)["items"].([]any) {
		r := it.(map[string]any)
		rules[r["rule_key"].(string)] = r
	}
	if len(rules) < 15 {
		t.Fatalf("应补建完整的触发器目录，实际 %d 条", len(rules))
	}
	for key, enabled := range map[string]bool{"reading.chapter": true, "community.comment": true, "creation.chapter_published": true, "community.reaction_received": false, "reading.annotation": false} {
		if rules[key]["enabled"] != enabled {
			t.Fatalf("规则 %s 默认启停应为 %v: %v", key, enabled, rules[key])
		}
	}

	// 停用的规则不发经验；启用后发放，且按来源 ID 去重
	received := plugincore.ActivityEvent{UserID: user.ID, Type: "reaction.received", SourceType: "reaction", SourceID: "7", DedupeKey: "reaction.received:7"}
	plugincore.FireActivity(e.app, received)
	if p := growth.Profile(e.app, user.ID); p.LifetimeXP != 0 {
		t.Fatalf("停用规则不应发经验，实际 %d", p.LifetimeXP)
	}
	ruleID := int(rules["community.reaction_received"]["id"].(float64))
	if status, payload := e.do(t, http.MethodPut, "/api/v1/admin/growth/rules/"+strconv.Itoa(ruleID), `{"base_xp":4,"daily_cap":0,"enabled":true}`); status != http.StatusOK {
		t.Fatalf("启用规则失败: %d %v", status, payload)
	}
	plugincore.FireActivity(e.app, received)
	plugincore.FireActivity(e.app, received)
	if p := growth.Profile(e.app, user.ID); p.LifetimeXP != 4 {
		t.Fatalf("启用后应发 4 经验且去重，实际 %d", p.LifetimeXP)
	}

	// 人工调整（按用户 ID），响应带用户名与最新经验
	status, payload = e.do(t, http.MethodPost, "/api/v1/admin/growth/adjust", `{"user_id":`+strconv.FormatUint(uint64(user.ID), 10)+`,"xp":96,"reason":"活动奖励"}`)
	if status != http.StatusOK || payload["data"].(map[string]any)["username"] != "growth-catalog" || payload["data"].(map[string]any)["lifetime_xp"].(float64) != 100 {
		t.Fatalf("人工调整失败: %d %v", status, payload)
	}

	// 经验流水：按用户、按规则筛选，附用户信息
	status, payload = e.do(t, http.MethodGet, "/api/v1/admin/growth/events?user_id="+strconv.FormatUint(uint64(user.ID), 10), "")
	data := payload["data"].(map[string]any)
	if status != http.StatusOK || int(data["total"].(float64)) != 2 {
		t.Fatalf("按用户筛选经验流水应有 2 条: %d %v", status, payload)
	}
	first := data["items"].([]any)[0].(map[string]any)
	if first["rule_key"] != "admin.adjust" || first["user"].(map[string]any)["username"] != "growth-catalog" {
		t.Fatalf("经验流水应倒序并附用户信息: %v", first)
	}
	_, payload = e.do(t, http.MethodGet, "/api/v1/admin/growth/events?rule_key=community.reaction_received", "")
	if int(payload["data"].(map[string]any)["total"].(float64)) != 1 {
		t.Fatalf("按规则筛选经验流水应有 1 条: %v", payload)
	}
}

func (e *testEnv) doAs(t *testing.T, u *models.User, method, path, body string) (int, map[string]any) {
	t.Helper()
	token, err := auth.GenerateToken(e.app.Config.Secret, u.ID, u.Username, u.Role)
	if err != nil {
		t.Fatal(err)
	}
	saved := e.token
	e.token = token
	defer func() { e.token = saved }()
	return e.do(t, method, path, body)
}

func (e *testEnv) enableRule(t *testing.T, key string, xp int) {
	t.Helper()
	e.do(t, http.MethodGet, "/api/v1/admin/growth/rules", "") // 补建目录规则
	var rule models.ExperienceRule
	if err := e.db.Where("rule_key = ?", key).First(&rule).Error; err != nil {
		t.Fatal(err)
	}
	if status, payload := e.do(t, http.MethodPut, "/api/v1/admin/growth/rules/"+strconv.Itoa(int(rule.ID)), `{"base_xp":`+strconv.Itoa(xp)+`,"daily_cap":0,"enabled":true}`); status != http.StatusOK {
		t.Fatalf("启用规则 %s 失败: %d %v", key, status, payload)
	}
}

// 取消点赞/收藏（真实路由）→ 收回点赞者与作者因此获得的经验：记等额负流水、只收回一次，可再次获得。
func TestDeletedContentRevokesExperience(t *testing.T) {
	e := newTestEnv(t)
	e.enableRule(t, "community.reaction", 2)
	e.enableRule(t, "community.reaction_received", 3)
	author := e.user(t, "growth-author")
	fan := e.user(t, "growth-fan")
	book := models.Book{Title: "R", Slug: "reaction-book", UserID: author.ID, Status: "published", IsPublic: true}
	e.db.Create(&book)
	path := "/api/v1/books/" + strconv.FormatUint(uint64(book.ID), 10) + "/reactions"

	if status, payload := e.doAs(t, fan, http.MethodPost, path, `{"type":"like"}`); status != http.StatusOK {
		t.Fatalf("点赞失败: %d %v", status, payload)
	}
	if growth.Profile(e.app, fan.ID).LifetimeXP != 2 || growth.Profile(e.app, author.ID).LifetimeXP != 3 {
		t.Fatalf("点赞应给点赞者 2、作者 3 经验")
	}
	if status, payload := e.doAs(t, fan, http.MethodDelete, path+"?type=like", ""); status != http.StatusOK {
		t.Fatalf("取消点赞失败: %d %v", status, payload)
	}
	if growth.Profile(e.app, fan.ID).LifetimeXP != 0 || growth.Profile(e.app, author.ID).LifetimeXP != 0 {
		t.Fatalf("取消点赞应收回双方经验: fan=%d author=%d", growth.Profile(e.app, fan.ID).LifetimeXP, growth.Profile(e.app, author.ID).LifetimeXP)
	}
	var revoked []models.ExperienceEvent
	e.db.Where("user_id = ? AND final_xp < 0", fan.ID).Find(&revoked)
	if len(revoked) != 1 || revoked[0].Reason != "revoked" || revoked[0].RuleKey != "community.reaction" {
		t.Fatalf("应留下一条收回流水: %+v", revoked)
	}
	// 重新点赞（新记录）可再次获得
	e.doAs(t, fan, http.MethodPost, path, `{"type":"like"}`)
	if growth.Profile(e.app, fan.ID).LifetimeXP != 2 {
		t.Fatalf("重新点赞应再次获得经验，实际 %d", growth.Profile(e.app, fan.ID).LifetimeXP)
	}
}

// 隐藏成长资料：无资料时隐藏也生效，且之后获得经验不会把资料改回公开；排行榜只列公开用户，本人仍可见自己的名次。
func TestLeaderboardAndPrivacy(t *testing.T) {
	e := newTestEnv(t)
	alice, bob, carol := e.user(t, "lb-alice"), e.user(t, "lb-bob"), e.user(t, "lb-carol")
	if status, _ := e.doAs(t, carol, http.MethodPut, "/api/v1/users/me/growth/display", `{"public":false}`); status != http.StatusOK {
		t.Fatalf("隐藏成长资料失败: %d", status)
	}
	plugincore.RecordExperience(e.app, alice.ID, "test.grant", "x", "1", "lb-a", 100, "")
	plugincore.RecordExperience(e.app, bob.ID, "test.grant", "x", "1", "lb-b", 50, "")
	plugincore.RecordExperience(e.app, carol.ID, "test.grant", "x", "1", "lb-c", 300, "")
	if growth.Profile(e.app, carol.ID).Public {
		t.Fatal("获得经验后隐藏的成长资料不应被改回公开")
	}

	for _, period := range []string{"all", "week", "month"} {
		status, payload := e.do(t, http.MethodGet, "/api/v1/growth/leaderboard?period="+period, "")
		if status != http.StatusOK {
			t.Fatalf("排行榜 %s 失败: %d %v", period, status, payload)
		}
		data := payload["data"].(map[string]any)
		items := data["items"].([]any)
		if int(data["total"].(float64)) != 2 || len(items) != 2 {
			t.Fatalf("排行榜 %s 应只含 2 位公开用户: %v", period, data)
		}
		first := items[0].(map[string]any)
		if first["user"].(map[string]any)["username"] != "lb-alice" || first["rank"].(float64) != 1 || first["xp"].(float64) != 100 || first["level"] == nil {
			t.Fatalf("排行榜 %s 第一名应为 lb-alice(100) 并带等级: %v", period, first)
		}
	}
	_, payload := e.doAs(t, carol, http.MethodGet, "/api/v1/growth/leaderboard", "")
	me := payload["data"].(map[string]any)["me"].(map[string]any)
	if me["rank"].(float64) != 1 || me["public"] != false || me["xp"].(float64) != 300 {
		t.Fatalf("隐藏用户应能看到自己的名次: %v", me)
	}

	// 升级通知带 i18n 键与参数（前端按界面语言渲染）
	var n models.Notification
	if err := e.db.Where("user_id = ? AND type = ?", alice.ID, "growth").Order("id DESC").First(&n).Error; err != nil {
		t.Fatalf("应有升级通知: %v", err)
	}
	if !strings.Contains(n.Payload, `"key":"notify.growth.levelUp"`) || !strings.Contains(n.Title, "Lv.2") {
		t.Fatalf("升级通知应带 i18n 键且兜底标题含等级名: %s / %s", n.Title, n.Payload)
	}
}

// 成长设置：排行榜开关（关闭后接口 404、公开站点配置同步为 false）、最少上榜经验（未达门槛不上榜、本人无名次）。
func TestLeaderboardSettings(t *testing.T) {
	e := newTestEnv(t)
	alice, bob := e.user(t, "set-alice"), e.user(t, "set-bob")
	plugincore.RecordExperience(e.app, alice.ID, "test.grant", "x", "1", "set-a", 100, "")
	plugincore.RecordExperience(e.app, bob.ID, "test.grant", "x", "1", "set-b", 20, "")

	_, site := e.do(t, http.MethodGet, "/api/v1/site", "")
	if site["data"].(map[string]any)["growth_leaderboard_enabled"] != true {
		t.Fatalf("默认应开放排行榜: %v", site["data"])
	}
	if status, payload := e.do(t, http.MethodPut, "/api/v1/admin/growth/settings", `{"leaderboard_enabled":true,"leaderboard_min_xp":50}`); status != http.StatusOK || payload["data"].(map[string]any)["leaderboard_min_xp"].(float64) != 50 {
		t.Fatalf("保存成长设置失败: %d %v", status, payload)
	}
	if status, _ := e.do(t, http.MethodPut, "/api/v1/admin/growth/settings", `{"leaderboard_enabled":true,"leaderboard_min_xp":0}`); status != http.StatusBadRequest {
		t.Fatalf("最少上榜经验 < 1 应 400，实际 %d", status)
	}
	_, payload := e.doAs(t, bob, http.MethodGet, "/api/v1/growth/leaderboard", "")
	data := payload["data"].(map[string]any)
	if int(data["total"].(float64)) != 1 || data["items"].([]any)[0].(map[string]any)["user"].(map[string]any)["username"] != "set-alice" {
		t.Fatalf("门槛 50 时只有 set-alice 上榜: %v", data)
	}
	if _, has := data["me"].(map[string]any)["rank"]; has {
		t.Fatalf("未达门槛的本人不应有名次: %v", data["me"])
	}

	e.do(t, http.MethodPut, "/api/v1/admin/growth/settings", `{"leaderboard_enabled":false,"leaderboard_min_xp":1}`)
	if status, _ := e.do(t, http.MethodGet, "/api/v1/growth/leaderboard", ""); status != http.StatusNotFound {
		t.Fatalf("关闭排行榜后接口应 404，实际 %d", status)
	}
	_, site = e.do(t, http.MethodGet, "/api/v1/site", "")
	_, settings := e.do(t, http.MethodGet, "/api/v1/growth/settings", "")
	if site["data"].(map[string]any)["growth_leaderboard_enabled"] != false || settings["data"].(map[string]any)["leaderboard_enabled"] != false {
		t.Fatalf("关闭后公开配置应为 false: site=%v settings=%v", site["data"], settings["data"])
	}
}

// 每日签到：首次签到得经验、同日重复签到幂等；连续签到满 N 天发连续奖励；关闭签到后 404；规则列表带近 7 天统计。
func TestDailyCheckin(t *testing.T) {
	e := newTestEnv(t)
	u := e.user(t, "checkin-user")

	status, payload := e.doAs(t, u, http.MethodPost, "/api/v1/users/me/checkin", "")
	data := payload["data"].(map[string]any)
	if status != http.StatusOK || data["already"] != false || data["xp_awarded"].(float64) != 5 || data["streak"].(float64) != 1 || data["checked_today"] != true {
		t.Fatalf("首次签到应得 5 经验、连续 1 天: %d %v", status, payload)
	}
	if days := data["days"].([]any); len(days) != 1 || days[0] != data["today"] {
		t.Fatalf("当月签到日历应含今天: %v", data)
	}
	_, payload = e.doAs(t, u, http.MethodPost, "/api/v1/users/me/checkin", "")
	data = payload["data"].(map[string]any)
	if data["already"] != true || data["xp_awarded"].(float64) != 0 || data["total_days"].(float64) != 1 {
		t.Fatalf("同日重复签到应幂等: %v", data)
	}

	// 连续签到：已连续 6 天（到昨天），今天签到达成 7 天 → 每日 5 + 连续奖励 20
	streaker := e.user(t, "checkin-streak")
	for i := 6; i >= 1; i-- {
		day := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		e.db.Create(&models.UserCheckin{UserID: streaker.ID, Day: day, Streak: 7 - i})
	}
	_, payload = e.doAs(t, streaker, http.MethodPost, "/api/v1/users/me/checkin", "")
	data = payload["data"].(map[string]any)
	if data["streak"].(float64) != 7 || data["milestone"] != true || data["xp_awarded"].(float64) != 25 || data["next_bonus_in"].(float64) != 7 {
		t.Fatalf("连续 7 天应触发连续奖励（共 25 经验）: %v", data)
	}
	_, payload = e.doAs(t, streaker, http.MethodGet, "/api/v1/users/me/checkin", "")
	if st := payload["data"].(map[string]any); st["longest_streak"].(float64) != 7 || st["total_days"].(float64) != 7 {
		t.Fatalf("签到状态统计错误: %v", st)
	}

	// 规则列表：签到规则默认启用，并带近 7 天发放统计
	_, payload = e.do(t, http.MethodGet, "/api/v1/admin/growth/rules", "")
	stats := payload["data"].(map[string]any)["stats_7d"].(map[string]any)
	if daily := stats["checkin.daily"].(map[string]any); daily["count"].(float64) != 2 || daily["xp"].(float64) != 10 {
		t.Fatalf("checkin.daily 近 7 天应发放 2 次共 10 经验: %v", stats)
	}

	// 关闭签到
	e.do(t, http.MethodPut, "/api/v1/admin/growth/settings", `{"checkin_enabled":false}`)
	if status, _ := e.doAs(t, e.user(t, "checkin-late"), http.MethodPost, "/api/v1/users/me/checkin", ""); status != http.StatusNotFound {
		t.Fatalf("关闭签到后应 404，实际 %d", status)
	}
	_, payload = e.do(t, http.MethodGet, "/api/v1/growth/settings", "")
	if payload["data"].(map[string]any)["checkin_enabled"] != false {
		t.Fatalf("公开设置应反映签到已关闭: %v", payload["data"])
	}
}
