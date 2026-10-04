// Package mail 提供找回密码等场景的邮件发送，支持两种驱动：
//   - log：仅打印到后端日志（开发期无 SMTP 时使用，重置链接可在日志中找到）
//   - smtp：标准 SMTP，587/25 端口自动尝试 STARTTLS，465 端口使用隐式 TLS
package mail

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"html"
	"log"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Config 发信配置（由站点设置解析而来）
type Config struct {
	Driver   string // log | smtp
	Host     string
	Port     int
	Username string
	Password string
	From     string // 发件人地址
}

// Sender 邮件发送器
type Sender interface {
	Send(to, subject, htmlBody string) error
}

// New 按配置构建发送器；未知驱动回退到 log
func New(cfg Config) Sender {
	if cfg.Driver == "smtp" && cfg.Host != "" && cfg.From != "" {
		return smtpSender{cfg: cfg}
	}
	return logSender{from: cfg.From}
}

// logSender 开发驱动：完整内容输出到后端日志
type logSender struct{ from string }

func (s logSender) Send(to, subject, body string) error {
	log.Printf("[mail] from=%s to=%s subject=%q\nbody:\n%s", s.from, to, subject, body)
	return nil
}

// smtpSender 标准 SMTP 驱动
type smtpSender struct{ cfg Config }

func (s smtpSender) Send(to, subject, body string) error {
	addr := net.JoinHostPort(s.cfg.Host, fmt.Sprintf("%d", s.cfg.Port))
	from := s.cfg.From
	msg := buildMessage(from, to, subject, body)

	auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
	if s.cfg.Port == 465 {
		// 隐式 TLS
		conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: s.cfg.Host})
		if err != nil {
			return fmt.Errorf("连接 SMTP 失败: %w", err)
		}
		client, err := smtp.NewClient(conn, s.cfg.Host)
		if err != nil {
			return fmt.Errorf("SMTP 握手失败: %w", err)
		}
		return sendWithClient(client, auth, from, to, msg)
	}

	// 25/587：明文连接，服务端支持则升级 STARTTLS
	client, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("连接 SMTP 失败: %w", err)
	}
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: s.cfg.Host}); err != nil {
			client.Close()
			return fmt.Errorf("STARTTLS 升级失败: %w", err)
		}
	}
	return sendWithClient(client, auth, from, to, msg)
}

func sendWithClient(client *smtp.Client, auth smtp.Auth, from, to string, msg []byte) error {
	defer client.Close()
	if err := client.Auth(auth); err != nil {
		// 未配置凭据时部分服务器允许匿名发信，忽略纯凭据类错误
		if auth != nil && !strings.Contains(err.Error(), "unrecognized") {
			return fmt.Errorf("SMTP 认证失败: %w", err)
		}
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("设置发件人失败: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("设置收件人失败: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("打开数据通道失败: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("写入邮件失败: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("发送邮件失败: %w", err)
	}
	return client.Quit()
}

// buildMessage 组装 HTML 邮件；中文主题按 RFC 2047 base64 编码
func buildMessage(from, to, subject, htmlBody string) []byte {
	headers := strings.Join([]string{
		"From: KnowForge <" + from + ">",
		"To: " + to,
		"Subject: =?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(subject)) + "?=",
		"Date: " + time.Now().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
		"Content-Type: text/html; charset=UTF-8",
		"Content-Transfer-Encoding: base64",
	}, "\r\n")
	encoded := base64.StdEncoding.EncodeToString([]byte(htmlBody))
	// base64 每 76 字符换行（RFC 2045）
	var wrapped strings.Builder
	for i := 0; i < len(encoded); i += 76 {
		end := i + 76
		if end > len(encoded) {
			end = len(encoded)
		}
		wrapped.WriteString(encoded[i:end])
		wrapped.WriteString("\r\n")
	}
	return []byte(headers + "\r\n\r\n" + wrapped.String())
}

// NotificationHTML 生成站内通知的邮件正文
// NotificationText 通知邮件的固定文案（按收件人语言渲染后传入）。
type NotificationText struct {
	Greeting    string // 如「你好，」
	ViewDetails string // 链接文字，如「查看详情」
	Footer      string // 页脚说明（已含站点名）
}

// NotificationHTMLWithText 以给定文案生成通知邮件正文（多语言）。
func NotificationHTMLWithText(title, link string, text NotificationText) string {
	action := ""
	if strings.TrimSpace(link) != "" {
		action = fmt.Sprintf(`<p><a href="%s">%s</a></p>`, html.EscapeString(link), html.EscapeString(text.ViewDetails))
	}
	return fmt.Sprintf(`<div style="max-width:480px;margin:0 auto;font-family:sans-serif">
<p>%s</p>
<p>%s</p>
%s
<p style="color:#888;font-size:12px">%s</p>
</div>`, html.EscapeString(text.Greeting), html.EscapeString(title), action, html.EscapeString(text.Footer))
}

// ActionEmail 带一个操作链接的系统邮件（邮箱激活、找回密码等）的文案，已按收件人语言渲染。
type ActionEmail struct {
	Greeting  string // 如「你好，」
	Intro     string // 说明与引导，如「点击下面的链接激活你的邮箱：」
	Link      string
	Note      string // 有效期、安全提示等
	Signature string // 落款（站点名）
}

// ActionEmailHTML 生成带操作链接的系统邮件正文（链接同时作为可见文本，便于复制）。
func ActionEmailHTML(e ActionEmail) string {
	safeLink := html.EscapeString(e.Link)
	return fmt.Sprintf(`<div style="max-width:480px;margin:0 auto;font-family:sans-serif">
<p>%s</p>
<p>%s</p>
<p><a href="%s">%s</a></p>
<p>%s</p>
<p>%s</p>
</div>`, html.EscapeString(e.Greeting), html.EscapeString(e.Intro), safeLink, safeLink, html.EscapeString(e.Note), html.EscapeString(e.Signature))
}

// DigestEmail 邮件摘要（每日 / 每周）的内容，文案已按收件人语言渲染。
type DigestEmail struct {
	Greeting    string
	Intro       string
	Sections    []DigestSection
	ViewAllText string
	ViewAllLink string
	Footer      string
	Unsubscribe string // 退订链接文字
	UnsubLink   string
}

// DigestSection 摘要中的一组内容（如「评论」「关注的书籍更新」「协作动态」）。
type DigestSection struct {
	Title string
	Items []DigestItem
	More  string // 未列出部分的说明，如「还有 3 条」
}

// DigestItem 一条内容；Link 为空时只显示文字。
type DigestItem struct {
	Text string
	Link string
}

// DigestHTML 生成邮件摘要正文。
func DigestHTML(e DigestEmail) string {
	var b strings.Builder
	b.WriteString(`<div style="max-width:560px;margin:0 auto;font-family:sans-serif;color:#1e293b">`)
	fmt.Fprintf(&b, `<p>%s</p><p>%s</p>`, html.EscapeString(e.Greeting), html.EscapeString(e.Intro))
	for _, s := range e.Sections {
		fmt.Fprintf(&b, `<h3 style="margin:24px 0 8px;font-size:15px">%s</h3><ul style="margin:0;padding-left:20px">`, html.EscapeString(s.Title))
		for _, it := range s.Items {
			if strings.TrimSpace(it.Link) != "" {
				fmt.Fprintf(&b, `<li style="margin:4px 0"><a href="%s" style="color:#4169e1;text-decoration:none">%s</a></li>`, html.EscapeString(it.Link), html.EscapeString(it.Text))
			} else {
				fmt.Fprintf(&b, `<li style="margin:4px 0">%s</li>`, html.EscapeString(it.Text))
			}
		}
		b.WriteString(`</ul>`)
		if s.More != "" {
			fmt.Fprintf(&b, `<p style="margin:4px 0 0;color:#64748b;font-size:13px">%s</p>`, html.EscapeString(s.More))
		}
	}
	if e.ViewAllLink != "" {
		fmt.Fprintf(&b, `<p style="margin-top:24px"><a href="%s" style="color:#4169e1">%s</a></p>`, html.EscapeString(e.ViewAllLink), html.EscapeString(e.ViewAllText))
	}
	fmt.Fprintf(&b, `<p style="color:#888;font-size:12px;margin-top:24px">%s`, html.EscapeString(e.Footer))
	if e.UnsubLink != "" {
		fmt.Fprintf(&b, ` · <a href="%s" style="color:#888">%s</a>`, html.EscapeString(e.UnsubLink), html.EscapeString(e.Unsubscribe))
	}
	b.WriteString(`</p></div>`)
	return b.String()
}
