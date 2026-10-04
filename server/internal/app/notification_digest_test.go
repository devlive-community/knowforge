package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
)

func TestDigestDue(t *testing.T) {
	monday8 := time.Date(2026, 10, 5, 8, 15, 0, 0, time.Local) // 周一 8:15
	tuesday8 := monday8.Add(24 * time.Hour)
	recent := monday8.Add(-2 * time.Hour)
	old := monday8.Add(-25 * time.Hour)
	weekAgo := monday8.Add(-7 * 24 * time.Hour)
	cases := []struct {
		mode string
		last *time.Time
		now  time.Time
		want bool
	}{
		{"daily", nil, monday8, true},
		{"daily", &old, tuesday8, true},
		{"daily", &recent, monday8, false},
		{"daily", nil, monday8.Add(2 * time.Hour), false}, // 不在 8 点
		{"weekly", &weekAgo, monday8, true},
		{"weekly", nil, tuesday8, false}, // 不是周一
		{"weekly", &old, monday8, false}, // 距上次不足 6 天
		{"instant", nil, monday8, false},
	}
	for i, tc := range cases {
		if got := digestDue(tc.mode, tc.last, tc.now); got != tc.want {
			t.Fatalf("case %d: digestDue(%s) = %v, want %v", i, tc.mode, got, tc.want)
		}
	}
}

// 邮件摘要：选择每日摘要后不再逐条发邮件；摘要汇总通知（遵守逐类型开关）与参与写作的书籍中他人的协作动态，
// 附一键退订链接；退订后邮件发送方式变为「不发送」。
func TestNotificationDigest(t *testing.T) {
	a, _, db := newContentImportTestApp(t)
	a.Notifications = newNotificationHub()
	mails := &captureMail{}
	a.MailSender = mails
	a.Config = &config.Config{Installed: true, Secret: "digest-test-secret"}
	_ = a.setSetting("mail_notifications_enabled", "true", "test")
	_ = a.setSetting("site_name", "KF", "test")
	_ = a.setSetting("site_url", "https://kf.test", "test")
	reader := models.User{Username: "dg-reader", Email: "dg@test.local", IsActive: true}
	editor := models.User{Username: "dg-editor", Email: "dg-editor@test.local", IsActive: true}
	db.Create(&reader)
	db.Create(&editor)
	a.saveNotificationPref(models.UserNotificationPref{UserID: reader.ID, Comment: true, Reaction: false, Collaboration: true, Moderation: true, System: true, Achievement: true, BookUpdate: true, Growth: true, DigestMode: "daily"})
	book := models.Book{Title: "合写", Slug: "dg-book", UserID: reader.ID, Status: "draft"}
	db.Create(&book)

	a.NotifyI18n(reader.ID, "comment", "notify.comment.chapter", map[string]string{"user": "bob", "chapter": "Intro"}, map[string]any{"link": "/x"})
	a.NotifyI18n(reader.ID, "reaction", "notify.comment.chapter", map[string]string{"user": "eve", "chapter": "Muted"}, nil) // 点赞类邮件已关闭
	if len(mails.subjects) != 0 {
		t.Fatalf("每日摘要模式下不应逐条发邮件: %v", mails.subjects)
	}
	a.recordActivity(book.ID, 1, editor.ID, "doc.saved", map[string]any{"title": "第一章", "added": 3, "removed": 1})
	a.recordActivity(book.ID, 1, editor.ID, "comment.created", map[string]any{"title": "第一章"})
	a.recordActivity(book.ID, 1, reader.ID, "doc.saved", map[string]any{"title": "第一章"}) // 自己的操作不计入

	now := time.Now().Add(time.Second)
	count, err := a.sendDigest(reader.ID, "daily", now.Add(-24*time.Hour), now)
	if err != nil || count != 2 || len(mails.subjects) != 1 {
		t.Fatalf("应发出 1 封摘要（1 条通知 + 1 本书的动态）: count=%d err=%v mails=%v", count, err, mails.subjects)
	}
	body := mails.bodies[0]
	for _, want := range []string{"每日摘要", "评论", "Intro", `href="https://kf.test/x"`, "协作动态", "《合写》：修改 1 次、批注 1 条", "https://kf.test/book/writer/dg-book?team=activity", "https://kf.test/email/unsubscribe?u="} {
		if !strings.Contains(mails.subjects[0]+body, want) {
			t.Fatalf("摘要中缺少 %q:\n%s\n%s", want, mails.subjects[0], body)
		}
	}
	if strings.Contains(body, "Muted") {
		t.Fatal("已关闭邮件的通知类型不应出现在摘要中")
	}
	// 没有新内容时不发送
	if count, _ := a.sendDigest(reader.ID, "daily", now, now.Add(time.Hour)); count != 0 || len(mails.subjects) != 1 {
		t.Fatalf("没有新内容时不应发送: %d", count)
	}

	// 一键退订
	router := a.Router()
	unsub := func(token string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/email/unsubscribe", bytes.NewReader([]byte(`{"u":`+strconv.Itoa(int(reader.ID))+`,"token":"`+token+`"}`)))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := unsub("forged"); code != http.StatusBadRequest {
		t.Fatalf("伪造的退订链接应拒绝: %d", code)
	}
	if code := unsub(a.digestUnsubToken(reader.ID)); code != http.StatusOK {
		t.Fatalf("退订失败: %d", code)
	}
	if p := a.notificationPref(reader.ID); p.DigestMode != "off" || p.Reaction {
		t.Fatalf("退订后应为不发送，其余偏好不变: %+v", p)
	}
}

// 首次保存偏好时关闭的开关必须存为 false（布尔列带 default:true，直接 Create 会被存成 true）。
func TestSaveNotificationPrefFirstInsert(t *testing.T) {
	a, _, _ := newContentImportTestApp(t)
	a.saveNotificationPref(models.UserNotificationPref{UserID: 4242, Comment: true, Reaction: false, Growth: false, DigestMode: "weekly"})
	if p := a.notificationPref(4242); !p.Comment || p.Reaction || p.Growth || p.Moderation || p.DigestMode != "weekly" {
		t.Fatalf("首次保存的偏好不对: %+v", p)
	}
	a.saveNotificationPref(models.UserNotificationPref{UserID: 4242, Reaction: true, DigestMode: "instant"})
	if p := a.notificationPref(4242); p.Comment || !p.Reaction || p.DigestMode != "instant" {
		t.Fatalf("再次保存的偏好不对: %+v", p)
	}
}
