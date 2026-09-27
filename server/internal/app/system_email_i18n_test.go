package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// 系统邮件（激活邮箱、找回密码）多语言：新用户按触发请求的界面语言；已设偏好语言的用户按偏好（优先于请求语言）；默认中文。
func TestSystemEmailsFollowRecipientLanguage(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	a.RateLimits = allowAllRateLimitStore{}
	mails := &captureMail{}
	a.MailSender = mails
	ts := httptest.NewServer(a.Router())
	defer ts.Close()
	client := &http.Client{Timeout: 10 * time.Second}
	post := func(path, lang string, body any) int {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, ts.URL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if lang != "" {
			req.Header.Set("Accept-Language", lang)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	lastMail := func() (string, string) {
		t.Helper()
		for i := 0; i < 10; i++ {
			if ran, err := a.Jobs.RunOnce(context.Background()); err != nil || !ran {
				break
			}
		}
		if len(mails.subjects) == 0 {
			t.Fatal("应发送邮件")
		}
		return mails.subjects[len(mails.subjects)-1], mails.bodies[len(mails.bodies)-1]
	}

	post("/api/v1/setup/install", "", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "KF"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	})
	_ = a.setSetting(cfgRegRequireEmail, "true", "test")
	_ = a.setSetting(cfgRegRequireActivation, "true", "test")

	// 新用户在英文界面注册 → 英文激活邮件
	if status := post("/api/v1/auth/register", "en-US,en;q=0.9", map[string]any{"username": "enuser", "email": "en@test.local", "password": "secret123"}); status != http.StatusOK {
		t.Fatalf("注册失败: %d", status)
	}
	subject, body := lastMail()
	if subject != "Verify your email for KF" || !strings.Contains(body, "Hi,") || !strings.Contains(body, "valid for 1440 minutes") {
		t.Fatalf("激活邮件应为英文: %q\n%s", subject, body)
	}

	// 注册时的界面语言记为偏好语言 → 之后后台触发的通知邮件也是英文
	var enUser models.User
	a.DB.Where("username = ?", "enuser").First(&enUser)
	if enUser.PreferredLocale != "en" {
		t.Fatalf("注册时的界面语言应记为偏好语言 en，实际 %q", enUser.PreferredLocale)
	}
	_ = a.setSetting("mail_notifications_enabled", "true", "test")
	a.NotifyI18n(enUser.ID, "comment", "notify.comment.reply", map[string]string{"user": "amy"}, nil)
	if subject, _ := lastMail(); subject != "[KF] amy replied to your comment" {
		t.Fatalf("偏好英文的新用户，通知邮件应为英文: %q", subject)
	}
	// 请求没有任何语言信号时不写入偏好（保持跟随站点默认语言）
	post("/api/v1/auth/register", "", map[string]any{"username": "nolang", "email": "nolang@test.local", "password": "secret123"})
	var noLang models.User
	a.DB.Where("username = ?", "nolang").First(&noLang)
	if noLang.PreferredLocale != "" {
		t.Fatalf("无语言信号时不应写入偏好语言，实际 %q", noLang.PreferredLocale)
	}

	// 中文界面找回密码（无偏好语言）→ 中文
	post("/api/v1/auth/password/forgot", "zh-CN,zh;q=0.9", map[string]any{"email": "admin@test.local"})
	if subject, body := lastMail(); subject != "重置你的 KF 密码" || !strings.Contains(body, "你好，") || !strings.Contains(body, "60 分钟内有效") {
		t.Fatalf("找回密码邮件应为中文: %q\n%s", subject, body)
	}

	// 用户偏好英文时优先于请求语言
	a.DB.Model(&models.User{}).Where("username = ?", "admin").Update("preferred_locale", "en")
	post("/api/v1/auth/password/forgot", "zh-CN", map[string]any{"email": "admin@test.local"})
	if subject, body := lastMail(); subject != "Reset your KF password" || !strings.Contains(body, "valid for 60 minutes") {
		t.Fatalf("偏好英文的用户应收到英文邮件: %q\n%s", subject, body)
	}
}
