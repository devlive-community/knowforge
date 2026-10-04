package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/cluster"
	"knowforge/server/internal/i18ntext"
	"knowforge/server/internal/mail"
	"knowforge/server/internal/models"
)

// —— 邮件摘要：用户可把通知邮件改为每日或每周汇总（或不发送邮件）。摘要在服务器时间每天 8 点（每周摘要为周一 8 点）发送，
// 内容为上次摘要以来的站内通知（按类型分组，遵守逐类型的邮件开关），以及用户参与写作的书籍中他人的协作动态汇总。
// 邮件附一键退订链接（签名，不需要登录）。——

const (
	digestHour          = 8
	digestCheckInterval = 10 * time.Minute
	digestItemsPerType  = 8
	digestUnsubPrefix   = "knowforge-digest-unsubscribe:"
)

var digestModes = map[string]bool{"instant": true, "daily": true, "weekly": true, "off": true}

// 通知类型在摘要中的顺序
var digestTypeOrder = []string{"collaboration", "comment", "book_update", "reaction", "moderation", "achievement", "growth", "system"}

func init() {
	for key, texts := range map[string][2]string{
		"email.digest.subject.daily":         {"你的每日摘要：{count} 条新消息", "Your daily digest: {count} new updates"},
		"email.digest.subject.weekly":        {"你的每周摘要：{count} 条新消息", "Your weekly digest: {count} new updates"},
		"email.digest.intro.daily":           {"这是过去一天里你在 {site} 的新消息：", "Here's what happened on {site} in the past day:"},
		"email.digest.intro.weekly":          {"这是过去一周里你在 {site} 的新消息：", "Here's what happened on {site} in the past week:"},
		"email.digest.section.collaboration": {"协作", "Collaboration"},
		"email.digest.section.comment":       {"评论", "Comments"},
		"email.digest.section.book_update":   {"关注的书籍更新", "Books you follow"},
		"email.digest.section.reaction":      {"点赞与收藏", "Likes and favorites"},
		"email.digest.section.moderation":    {"审核", "Moderation"},
		"email.digest.section.achievement":   {"成就", "Achievements"},
		"email.digest.section.growth":        {"成长", "Growth"},
		"email.digest.section.system":        {"系统", "System"},
		"email.digest.section.other":         {"其他", "Other"},
		"email.digest.section.activity":      {"协作动态", "Writing activity"},
		"email.digest.more":                  {"还有 {count} 条", "and {count} more"},
		"email.digest.viewAll":               {"查看全部通知", "View all notifications"},
		"email.digest.footer":                {"你收到这封邮件是因为在 {site} 选择了邮件摘要，可在「通知设置」中修改。", "You're receiving this because you chose email digests on {site}. Change this in your notification settings."},
		"email.digest.unsubscribe":           {"退订邮件", "Unsubscribe"},
		"email.digest.activity.book":         {"《{book}》：{summary}", "\"{book}\": {summary}"},
		"email.digest.activity.edits":        {"修改 {count} 次", "{count} edits"},
		"email.digest.activity.chapters":     {"增删章节 {count} 次", "{count} chapter changes"},
		"email.digest.activity.comments":     {"批注 {count} 条", "{count} comments"},
		"email.digest.activity.suggestions":  {"修改建议 {count} 条", "{count} suggestions"},
		"email.digest.activity.tasks":        {"分工调整 {count} 次", "{count} assignment changes"},
		"email.digest.activity.separator":    {"、", ", "},
	} {
		i18ntext.Register(key, map[string]string{"zh-CN": texts[0], "en": texts[1]})
	}
}

func (a *App) notificationPref(userID uint) models.UserNotificationPref {
	p := models.UserNotificationPref{UserID: userID, Comment: true, Reaction: true, Collaboration: true, Moderation: true, System: true, Achievement: true, BookUpdate: true, Growth: true, DigestMode: "instant"}
	a.DB.Where("user_id = ?", userID).First(&p)
	if !digestModes[p.DigestMode] {
		p.DigestMode = "instant"
	}
	return p
}

// digestDue 是否到了发送摘要的时间：每日摘要在每天 8 点、每周摘要在周一 8 点，且距上次足够久（避免同一时段重复发送）。
func digestDue(mode string, last *time.Time, now time.Time) bool {
	if now.Hour() != digestHour {
		return false
	}
	switch mode {
	case "daily":
		return last == nil || now.Sub(*last) > 20*time.Hour
	case "weekly":
		return now.Weekday() == time.Monday && (last == nil || now.Sub(*last) > 6*24*time.Hour)
	}
	return false
}

// sendDigestsIfDue 由后台每 10 分钟调用：多实例时只由持有租约的实例发送。
func (a *App) sendDigestsIfDue(ctx context.Context) {
	if !a.mailNotificationsEnabled() || currentTime().Hour() != digestHour {
		return
	}
	if !cluster.TryLease("notification-digest", digestCheckInterval+5*time.Minute) {
		return
	}
	now := currentTime()
	var prefs []models.UserNotificationPref
	a.DB.WithContext(ctx).Where("digest_mode IN ?", []string{"daily", "weekly"}).Find(&prefs)
	for i := range prefs {
		p := &prefs[i]
		if ctx.Err() != nil || !digestDue(p.DigestMode, p.LastDigestAt, now) {
			continue
		}
		since := now.Add(-24 * time.Hour)
		if p.DigestMode == "weekly" {
			since = now.Add(-7 * 24 * time.Hour)
		}
		if p.LastDigestAt != nil && p.LastDigestAt.After(since) {
			since = *p.LastDigestAt
		}
		if _, err := a.sendDigest(p.UserID, p.DigestMode, since, now); err != nil {
			log.Printf("[digest] 发送摘要失败 user=%d: %v", p.UserID, err)
		}
		a.DB.Model(&models.UserNotificationPref{}).Where("user_id = ?", p.UserID).Update("last_digest_at", now)
	}
}

// sendDigest 汇总 since 以来的通知与协作动态并发送摘要邮件；没有内容时不发送。返回邮件中的条目数。
func (a *App) sendDigest(userID uint, mode string, since, now time.Time) (int, error) {
	var u models.User
	if a.DB.First(&u, userID).Error != nil || u.Email == "" {
		return 0, errors.New("用户没有邮箱")
	}
	chain := a.userLocaleChain(&u)
	base := strings.TrimRight(a.getSetting("site_url"), "/")
	absolute := func(link string) string {
		if link == "" || strings.HasPrefix(link, "http") {
			return link
		}
		if base == "" {
			return ""
		}
		return base + link
	}
	siteName := strings.TrimSpace(a.getSetting("site_name"))
	if siteName == "" {
		siteName = "KnowForge"
	}

	// 通知：按类型分组（遵守逐类型的邮件开关）
	var notes []models.Notification
	a.DB.Where("user_id = ? AND created_at > ? AND created_at <= ?", u.ID, since, now).Order("created_at DESC").Limit(500).Find(&notes)
	groups := map[string][]mail.DigestItem{}
	counts := map[string]int{}
	total := 0
	for _, n := range notes {
		if !a.emailPrefFor(&u, n.Type) {
			continue
		}
		key := n.Type
		if !containsString(digestTypeOrder, key) {
			key = "other"
		}
		counts[key]++
		total++
		if len(groups[key]) >= digestItemsPerType {
			continue
		}
		payload := map[string]any{}
		_ = json.Unmarshal([]byte(n.Payload), &payload)
		title := n.Title
		if k, params, found := notificationI18n(payload); found {
			title = a.renderText(k, params, chain)
		}
		link, _ := payload["link"].(string)
		groups[key] = append(groups[key], mail.DigestItem{Text: title, Link: absolute(link)})
	}
	sections := []mail.DigestSection{}
	for _, key := range append(append([]string{}, digestTypeOrder...), "other") {
		if len(groups[key]) == 0 {
			continue
		}
		s := mail.DigestSection{Title: a.renderText("email.digest.section."+key, nil, chain), Items: groups[key]}
		if extra := counts[key] - len(groups[key]); extra > 0 {
			s.More = a.renderText("email.digest.more", map[string]string{"count": strconv.Itoa(extra)}, chain)
		}
		sections = append(sections, s)
	}

	// 协作动态：参与写作的书籍中他人的操作，按书汇总
	if activity := a.digestActivity(&u, since, now, chain, absolute); len(activity.Items) > 0 {
		sections = append(sections, activity)
		total += len(activity.Items)
	}
	if total == 0 {
		return 0, nil
	}

	email := mail.DigestEmail{
		Greeting:    a.renderText("email.common.greeting", nil, chain),
		Intro:       a.renderText("email.digest.intro."+mode, map[string]string{"site": siteName}, chain),
		Sections:    sections,
		ViewAllText: a.renderText("email.digest.viewAll", nil, chain),
		ViewAllLink: absolute("/notifications"),
		Footer:      a.renderText("email.digest.footer", map[string]string{"site": siteName}, chain),
		Unsubscribe: a.renderText("email.digest.unsubscribe", nil, chain),
	}
	if base != "" {
		email.UnsubLink = fmt.Sprintf("%s/email/unsubscribe?u=%d&token=%s", base, u.ID, a.digestUnsubToken(u.ID))
	}
	subject := "[" + siteName + "] " + a.renderText("email.digest.subject."+mode, map[string]string{"count": strconv.Itoa(total)}, chain)
	subject = strings.NewReplacer("\r", " ", "\n", " ").Replace(subject)
	return total, a.enqueueEmail(context.Background(), u.Email, subject, mail.DigestHTML(email))
}

// digestActivity 用户参与写作（作者、编辑者、建议者）的书籍中，他人在这段时间的协作动态（每本书一条汇总）。
func (a *App) digestActivity(u *models.User, since, now time.Time, chain []string, absolute func(string) string) mail.DigestSection {
	section := mail.DigestSection{Title: a.renderText("email.digest.section.activity", nil, chain)}
	var bookIDs []uint
	a.DB.Model(&models.Book{}).Where("user_id = ?", u.ID).Pluck("id", &bookIDs)
	var collab []uint
	a.DB.Model(&models.BookCollaborator{}).Where("user_id = ? AND status = ? AND role IN ?", u.ID, "accepted", []string{"editor", "suggester"}).Pluck("book_id", &collab)
	bookIDs = append(bookIDs, collab...)
	if len(bookIDs) == 0 {
		return section
	}
	var rows []struct {
		BookID uint
		Kind   string
		N      int
	}
	a.DB.Model(&models.WriterActivity{}).Select("book_id, kind, COUNT(*) AS n").
		Where("book_id IN ? AND user_id <> ? AND updated_at > ? AND updated_at <= ?", bookIDs, u.ID, since, now).
		Group("book_id, kind").Scan(&rows)
	perBook := map[uint]map[string]int{}
	order := []uint{}
	for _, r := range rows {
		if perBook[r.BookID] == nil {
			perBook[r.BookID] = map[string]int{}
			order = append(order, r.BookID)
		}
		category := "edits"
		switch {
		case r.Kind == "doc.created" || r.Kind == "doc.deleted" || r.Kind == "doc.restored":
			category = "chapters"
		case strings.HasPrefix(r.Kind, "comment."):
			category = "comments"
		case strings.HasPrefix(r.Kind, "suggestion."):
			category = "suggestions"
		case strings.HasPrefix(r.Kind, "task."):
			category = "tasks"
		}
		perBook[r.BookID][category] += r.N
	}
	sep := a.renderText("email.digest.activity.separator", nil, chain)
	for _, id := range order {
		var book models.Book
		if a.DB.Select("id", "title", "slug").First(&book, id).Error != nil {
			continue
		}
		parts := []string{}
		for _, category := range []string{"edits", "chapters", "comments", "suggestions", "tasks"} {
			if n := perBook[id][category]; n > 0 {
				parts = append(parts, a.renderText("email.digest.activity."+category, map[string]string{"count": strconv.Itoa(n)}, chain))
			}
		}
		section.Items = append(section.Items, mail.DigestItem{
			Text: a.renderText("email.digest.activity.book", map[string]string{"book": book.Title, "summary": strings.Join(parts, sep)}, chain),
			Link: absolute("/book/writer/" + book.Slug + "?team=activity"),
		})
	}
	return section
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (a *App) digestUnsubToken(userID uint) string {
	mac := hmac.New(sha256.New, []byte(a.Config.Secret))
	mac.Write([]byte(digestUnsubPrefix + strconv.FormatUint(uint64(userID), 10)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// SendDigestPreview POST /auth/notification-prefs/digest-preview 立即给自己发一封最近 7 天的摘要（不影响定时摘要）。
func (a *App) SendDigestPreview(c *gin.Context) {
	if !a.mailNotificationsEnabled() {
		fail(c, http.StatusBadRequest, "站点未开启邮件通知")
		return
	}
	u := currentUser(c)
	mode := a.notificationPref(u.ID).DigestMode
	if mode != "daily" && mode != "weekly" {
		mode = "weekly"
	}
	now := currentTime()
	count, err := a.sendDigest(u.ID, mode, now.Add(-7*24*time.Hour), now)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"sent": count > 0, "count": count})
}

// UnsubscribeDigest POST /email/unsubscribe {u, token} 邮件中的一键退订：把邮件发送方式改为「不发送」（不需要登录）。
func (a *App) UnsubscribeDigest(c *gin.Context) {
	var req struct {
		U     uint   `json:"u"`
		Token string `json:"token"`
	}
	if c.ShouldBindJSON(&req) != nil || req.U == 0 || !hmac.Equal([]byte(req.Token), []byte(a.digestUnsubToken(req.U))) {
		fail(c, http.StatusBadRequest, "退订链接无效")
		return
	}
	p := a.notificationPref(req.U)
	p.DigestMode = "off"
	if err := a.saveNotificationPref(p); err != nil {
		fail(c, http.StatusInternalServerError, "退订失败")
		return
	}
	ok(c, gin.H{"digest_mode": "off"})
}
