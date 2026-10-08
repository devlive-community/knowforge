package app

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// M15 管理端：SMTP 与站点地址配置（凭据存站点配置表，不出现在公开 /site）

var mailDrivers = map[string]bool{"log": true, "smtp": true}

type mailConfigUpdate struct {
	Driver               *string `json:"driver"`
	Host                 *string `json:"host"`
	Port                 *int    `json:"port"`
	Username             *string `json:"username"`
	Password             *string `json:"password"`
	From                 *string `json:"from"`
	SiteURL              *string `json:"site_url"`
	NotificationsEnabled *bool   `json:"notifications_enabled"`
}

// AdminGetMail GET /admin/mail 管理员读取邮件配置
func (a *App) AdminGetMail(c *gin.Context) {
	port, _ := strconv.Atoi(a.getSetting("smtp_port"))
	ok(c, gin.H{
		"driver":                a.getSetting("mail_driver"),
		"host":                  a.getSetting("smtp_host"),
		"port":                  port,
		"username":              a.getSetting("smtp_username"),
		"password":              a.getSetting("smtp_password"),
		"from":                  a.getSetting("smtp_from"),
		"site_url":              a.getSetting("site_url"),
		"notifications_enabled": a.getSetting("mail_notifications_enabled") == "true",
	})
}

// AdminSaveMail PUT /admin/mail 管理员保存邮件配置
func (a *App) AdminSaveMail(c *gin.Context) {
	var req mailConfigUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	fields := []string{}
	if req.Driver != nil {
		fields = append(fields, "driver")
		driver := *req.Driver
		if !mailDrivers[driver] {
			fail(c, http.StatusBadRequest, "发信驱动必须为 log 或 smtp")
			return
		}
		if err := a.setSetting("mail_driver", driver, "邮件驱动 log|smtp"); err != nil {
			fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
	}
	if req.Host != nil {
		fields = append(fields, "host")
		if err := a.setSetting("smtp_host", *req.Host, "SMTP 主机"); err != nil {
			fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
	}
	if req.Port != nil {
		fields = append(fields, "port")
		if *req.Port < 0 || *req.Port > 65535 {
			fail(c, http.StatusBadRequest, "SMTP 端口不合法")
			return
		}
		if err := a.setSetting("smtp_port", strconv.Itoa(*req.Port), "SMTP 端口"); err != nil {
			fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
	}
	if req.Username != nil {
		fields = append(fields, "username")
		if err := a.setSetting("smtp_username", *req.Username, "SMTP 用户名"); err != nil {
			fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
	}
	if req.Password != nil {
		fields = append(fields, "password")
		if err := a.setSetting("smtp_password", *req.Password, "SMTP 密码"); err != nil {
			fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
	}
	if req.From != nil {
		fields = append(fields, "from")
		if err := a.setSetting("smtp_from", *req.From, "发件人地址"); err != nil {
			fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
	}
	if req.SiteURL != nil {
		fields = append(fields, "site_url")
		previousSiteURL := a.getSetting("site_url")
		if err := a.setSetting("site_url", *req.SiteURL, "站点访问地址（用于邮件链接与 sitemap 生成）"); err != nil {
			fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
		if previousSiteURL != *req.SiteURL {
			// 地址变更后用新主机重新排队当前全部公开 URL，并立即重建 Sitemap。
			urls := a.indexableSiteURLs()
			if len(urls) > 0 {
				a.emitIndexableURLs(urls...)
			} else {
				a.enqueueSitemapGenerate()
			}
		}
	}
	if req.NotificationsEnabled != nil {
		fields = append(fields, "notifications_enabled")
		v := "false"
		if *req.NotificationsEnabled {
			v = "true"
		}
		if err := a.setSetting("mail_notifications_enabled", v, "站内通知同时发邮件的总开关"); err != nil {
			fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
	}
	a.recordAudit(c, "mail.updated", "config", "mail", "邮件配置", map[string]any{"changed_fields": fields})
	ok(c, gin.H{"message": "已保存"})
}
