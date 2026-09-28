package app

import (
	"net/http"
	"strings"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/plugincore"

	"github.com/gin-gonic/gin"
)

// installGate 未安装时拦截除安装向导外的全部业务 API
func (a *App) installGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		if a.Config.Installed || strings.HasPrefix(c.Request.URL.Path, "/api/v1/setup/") {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"code":    "NOT_INSTALLED",
			"message": "系统尚未安装，请先完成安装向导",
		})
	}
}

// Router 组装全部路由。
//
// 路由按功能域分组注册（详见 docs/api.md）：
//
//	基础     健康检查（无业务权限）
//	安装向导  仅未安装时可用
//	认证     注册/登录/会话
//	公开内容  站点配置、统计、发现、书籍/文档只读、用户主页（匿名可访问，可选登录扩大可见范围）
//	书籍管理  book:*
//	文档管理  document:*
//	用户     user:*
//	上传     upload:*
//	站点与系统管理  site:* / system:*（仅管理员）
func (a *App) Router() *gin.Engine {
	r := gin.New()
	configureTrustedProxies(r)
	r.Use(gin.Logger(), gin.Recovery(), CORS())
	r.MaxMultipartMemory = 10 << 20

	// ── 基础：健康检查（CI 与 nginx 使用，无业务权限） ──
	r.GET("/health", a.Health)
	r.GET("/api/v1/health", a.Health)

	a.ServeUploads(r)

	api := r.Group("/api/v1", a.installGate())
	api.GET("/i18n/locales", a.OptionalAuth(), a.I18nLocales)
	api.GET("/i18n/messages/:locale", a.I18nMessages)
	api.PUT("/auth/locale", a.RequireAuth(), a.RequirePermission(authz.UserUpdate), a.UpdateUserLocale)
	i18nAdmin := api.Group("/admin/i18n", a.RequireAuth(), a.RequireAdmin())
	i18nAdmin.GET("/locales", a.RequirePermission(authz.I18nManage), a.AdminI18nLocales)
	i18nAdmin.PUT("/locales", a.RequirePermission(authz.I18nManage), a.AdminSaveI18nLocales)
	i18nAdmin.GET("/messages/:locale", a.RequirePermission(authz.I18nManage), a.AdminI18nMessages)
	i18nAdmin.PUT("/messages/:locale", a.RequirePermission(authz.I18nManage), a.AdminSaveI18nMessages)
	{
		// ── 安装向导（仅未安装时可用，无业务权限） ──
		setup := api.Group("/setup")
		{
			setup.GET("/status", a.SetupStatus)
			setup.POST("/test-connection", a.SetupTest)
			setup.POST("/install", a.SetupInstall)
		}

		// ── 认证与会话 ──
		authGroup := api.Group("/auth")
		{
			authGroup.GET("/registration", a.PublicRegistrationInfo) // 注册页读取注册方式/邮箱要求
			api.GET("/captcha", a.NewCaptcha)                        // 场景验证码：required=false 或挑战
			authGroup.POST("/register", a.RateLimit(registerRateLimit), a.Register)
			authGroup.POST("/login", a.RateLimit(loginRateLimit), a.Login)

			// ── 找回密码（auth:password-reset 匿名语义） ──
			authGroup.POST("/password/forgot", a.RateLimit(passwordForgotRateLimit), a.ForgotPassword)
			authGroup.POST("/password/reset", a.RateLimit(passwordResetRateLimit), a.ResetPassword)

			// ── 邮箱激活（verify 匿名凭令牌；resend 需登录） ──
			authGroup.POST("/email/verify", a.RateLimit(passwordResetRateLimit), a.VerifyEmail)

			// ── 第三方登录（auth:oauth；start/callback 匿名，绑定管理需登录） ──
			authGroup.GET("/oauth/providers", a.OAuthProviders)
			authGroup.GET("/oauth/:provider", a.OAuthStart)
			authGroup.GET("/oauth/:provider/callback", a.OAuthCallback)
			authed2 := authGroup.Group("", a.RequireAuth())
			{
				authed2.GET("/oauth/bindings", a.RequirePermission(authz.AuthOauth), a.OAuthBindings)
				authed2.POST("/oauth/:provider/link", a.RequirePermission(authz.AuthOauth), a.OAuthLinkStart)
				authed2.DELETE("/oauth/:provider", a.RequirePermission(authz.AuthOauth), a.OAuthUnbind)
			}

			authed := authGroup.Group("", a.RequireAuth())
			{
				authed.GET("/me", a.Me)
				authed.GET("/permissions", a.CurrentPermissions)   // 当前用户权限列表
				authed.GET("/invite-code", a.MyInviteCode)         // 我的邀请码（未开启为空）
				authed.POST("/invite-code", a.EnableInviteCode)    // 开启专属邀请码
				authed.DELETE("/invite-code", a.DisableInviteCode) // 关闭邀请码
				authed.GET("/invited", a.MyInvitedUsers)           // 我邀请的用户列表
				authed.GET("/notification-prefs", a.GetNotificationPrefs)
				authed.PUT("/notification-prefs", a.UpdateNotificationPrefs)
				authed.POST("/email/resend", a.ResendActivation) // 重新发送激活邮件
				// ── 二次认证（TOTP） ──
				authed.GET("/2fa", a.GetTwoFactor)
				authed.POST("/2fa/setup", a.SetupTwoFactor)
				authed.POST("/2fa/enable", a.EnableTwoFactor)
				authed.POST("/2fa/disable", a.DisableTwoFactor)
				authed.PUT("/2fa/operations", a.UpdateTwoFactorOps)
				authed.POST("/2fa/verify", a.VerifyTwoFactor)
				authed.POST("/2fa/backup-codes", a.RegenerateBackupCodes)
				authed.PUT("/profile", a.RequirePermission(authz.UserUpdate), a.UpdateProfile)
				authed.PUT("/password", a.RequirePermission(authz.UserUpdate), a.ChangePassword)
				// 个人危险区：自助注销账号（冷静期）
				authed.GET("/account/deletion", a.RequirePermission(authz.UserRead), a.GetAccountDeletion)
				authed.POST("/account/deletion", a.RequirePermission(authz.UserUpdate), a.RequestAccountDeletion)
				authed.DELETE("/account/deletion", a.RequirePermission(authz.UserUpdate), a.CancelAccountDeletion)
				// 导出样式偏好（PDF 导出用）
				authed.GET("/export-settings", a.RequirePermission(authz.UserRead), a.GetExportSettings)
				authed.PUT("/export-settings", a.RequirePermission(authz.UserUpdate), a.UpdateExportSettings)
				// 主题设置
				authed.GET("/theme-settings", a.RequirePermission(authz.UserRead), a.GetThemeSettings)
				// 个人访问令牌（令牌本身不能访问这些接口）
				authed.GET("/tokens", a.MyAccessTokens)
				authed.GET("/tokens/permissions", a.TokenPermissions)
				authed.POST("/tokens", a.CreateAccessToken)
				authed.DELETE("/tokens/:id", a.RevokeAccessToken)
				authed.PUT("/theme-settings", a.RequirePermission(authz.UserUpdate), a.UpdateThemeSettings)
			}
		}

		// ── 公开内容：匿名可访问，可选登录以查看自己可见的私有内容 ──
		// 语义权限：site:read / stats:read / book:read / document:read / user:read。
		// 涉及登录用户可见私有内容的接口用 DeclarePermission 声明，限定权限的访问令牌缺少该权限时按游客处理。
		public := api.Group("", a.OptionalAuth())
		readBook, readDoc, exportBook := a.DeclarePermission(authz.BookRead), a.DeclarePermission(authz.DocumentRead), a.DeclarePermission(authz.BookExport)
		{
			public.GET("/site", a.GetSiteConfig)
			public.GET("/stats", a.SiteStats)
			public.GET("/sitemap", a.SitemapURLs)
			public.GET("/explore/hot", a.ExploreHot)
			public.GET("/explore/active-authors", a.ExploreActiveAuthors)
			public.GET("/explore/latest", a.ExploreLatest)
			public.GET("/users/:username", a.GetUserProfile)
			public.GET("/users/:username/books", readBook, a.GetUserBooks)
			// /growth/settings、/growth/levels、/users/:username/growth 由 growth 插件子包自注册

			public.GET("/books", readBook, a.ListBooks) // mine=true 时要求登录
			public.GET("/books/:id", readBook, a.GetBook)
			public.GET("/books/slug/:slug", readBook, a.GetBookBySlug)
			public.GET("/books/:id/documents", readDoc, a.ListDocumentTree)
			// /books/:id/translations 与 /books/:id/versions 由 book-translations / book-versions 插件子包自注册
			public.GET("/books/:id/documents/slug/:slug", readDoc, a.GetDocumentBySlug)
			public.GET("/documents/:id", readDoc, a.GetDocument)
			public.GET("/books/:id/related", readBook, a.RelatedBooks)        // 语义相关书籍（插件提供，没有时为空）
			public.GET("/documents/:id/related", readDoc, a.RelatedDocuments) // 语义相关章节
			// /tags、/tags/:slug/books 由 tags 插件子包自注册
			public.POST("/books/:id/view", a.IncrementBookView)
			// 导出（公开且开放导出的书籍匿名可导，鉴权在 handler 内）
			public.GET("/export/pdf-available", a.PDFExportAvailable)
			public.GET("/books/:id/export/options", exportBook, a.GetExportOptions)
			public.GET("/books/:id/export/markdown", exportBook, a.ExportBookMarkdownPublic)
			public.GET("/books/:id/export/epub", exportBook, a.ExportBookEPUB)
			public.GET("/books/:id/export/docx", exportBook, a.ExportBookDOCX)
			// /books/:id/export/pdf 由 pdf-export 插件子包注册
			public.POST("/documents/:id/view", a.IncrementDocumentView)
		}

		// ── 书籍管理（book:*，归属校验在 handler 内） ──
		books := api.Group("/books", a.RequireAuth())
		{
			books.POST("", a.RequireEmailVerified(), a.RequirePermission(authz.BookCreate), a.CreateBook)
			books.GET("/status-counts", a.RequirePermission(authz.BookRead), a.MyBookCounts)
			books.GET("/slug/:slug/access", a.RequirePermission(authz.BookRead), a.GetBookAccess)
			books.PUT("/:id", a.RequirePermission(authz.BookUpdate), a.UpdateBook)
			books.DELETE("/:id", a.RequirePermission(authz.BookDelete), a.DeleteBook)
			books.POST("/:id/copy", a.RequirePermission(authz.BookCreate), a.CopyBook)
			books.GET("/:id/export", a.RequirePermission(authz.BookExport), a.ExportBook)
			books.POST("/:id/import/pdf", a.RequirePermission(authz.BookImport), a.ReimportPDFBook)
			books.POST("/:id/documents/import-markdown", a.RequirePermission(authz.DocumentCreate), a.ImportMarkdownDocuments)
			books.POST("/:id/cleanup/permalink-anchors", a.RequirePermission(authz.BookUpdate), a.CleanupBookPermalinks)
			books.POST("/:id/cleanup/localize-images", a.RequirePermission(authz.BookUpdate), a.LocalizeBookImages)
			books.GET("/:id/read-chapters", a.RequirePermission(authz.UserRead), a.ReadChapters)
			books.GET("/:id/export-style", a.RequirePermission(authz.BookUpdate), a.GetBookExportStyle)
			books.PUT("/:id/export-style", a.RequirePermission(authz.BookUpdate), a.UpdateBookExportStyle)
			books.GET("/:id/analytics", a.RequirePermission(authz.BookAnalyticsRead), a.GetBookAnalytics)

			// ── 协作者管理（collaborator:*；归属校验在 handler 内） ──
			books.GET("/:id/collaborators", a.RequirePermission(authz.CollaboratorRead), a.ListCollaborators)
			books.POST("/:id/collaborators", a.RequirePermission(authz.CollaboratorCreate), a.AddCollaborator)
			books.DELETE("/:id/collaborators/:userId", a.RequirePermission(authz.CollaboratorDelete), a.RemoveCollaborator)
		}

		// ── 回收站（trash:*；恢复与永久删除继续执行对象级归属校验） ──
		trash := api.Group("/trash", a.RequireAuth())
		{
			trash.GET("", a.RequirePermission(authz.TrashRead), a.ListTrash)
			trash.POST("/books/:id/restore", a.RequirePermission(authz.TrashRestore), a.RestoreTrashedBook)
			trash.DELETE("/books/:id", a.RequirePermission(authz.TrashDelete), a.PermanentlyDeleteBook)
			trash.POST("/documents/:id/restore", a.RequirePermission(authz.TrashRestore), a.RestoreTrashedDocument)
			trash.DELETE("/documents/:id", a.RequirePermission(authz.TrashDelete), a.PermanentlyDeleteDocument)
		}

		// ── 文档管理（document:*，归属校验在 handler 内） ──
		docs := api.Group("", a.RequireAuth())
		{
			docs.POST("/books/:id/documents", a.RequireEmailVerified(), a.RequirePermission(authz.DocumentCreate), a.CreateDocument)
			docs.POST("/books/:id/documents/copy", a.RequirePermission(authz.DocumentCreate), a.CopyDocuments)
			docs.PUT("/documents/:id", a.RequirePermission(authz.DocumentUpdate), a.UpdateDocument)
			docs.DELETE("/documents/:id", a.RequirePermission(authz.DocumentDelete), a.DeleteDocument)
			docs.GET("/documents/:id/revisions", a.RequirePermission(authz.DocumentRevisionRead), a.ListDocumentRevisions)
			docs.GET("/documents/:id/revisions/:revisionId", a.RequirePermission(authz.DocumentRevisionRead), a.GetDocumentRevision)
			docs.POST("/documents/:id/revisions/:revisionId/restore", a.RequirePermission(authz.DocumentRevisionRestore), a.RestoreDocumentRevision)
		}

		// ── 全文搜索（search:read，匿名可搜公开内容） ──
		api.GET("/search", a.OptionalAuth(), a.GlobalSearch)
		api.GET("/search/semantic", a.OptionalAuth(), a.SemanticSearch)
		api.GET("/users/me/files", a.RequireAuth(), a.MyFiles)
		api.DELETE("/users/me/files/:id", a.RequireAuth(), a.DeleteMyFile)

		// ── 权益：定义（供等级/会员权益编辑器）与本人生效值 ──
		api.GET("/entitlements/definitions", a.RequireAuth(), a.EntitlementDefinitions)
		api.GET("/users/me/entitlements", a.RequireAuth(), a.MyEntitlements)
		api.GET("/users/me/ai-usage", a.RequireAuth(), a.MyAIUsage)
		api.GET("/users/me/ai-usage/logs", a.RequireAuth(), a.MyAIUsageLogs)
		api.GET("/users/me/ai-usage/export", a.RequireAuth(), a.ExportMyAIUsage)

		// ── 导入书籍（book:import，ZIP / PDF 成为本人的书籍；网页导入/采集由 content-collect 插件子包注册） ──
		api.POST("/import", a.RequireAuth(), a.RequirePermission(authz.BookImport), a.ImportBook)
		api.POST("/import/pdf", a.RequireAuth(), a.RequirePermission(authz.BookImport), a.ImportPDFBook)
		api.POST("/import/markdown", a.RequireAuth(), a.RequirePermission(authz.BookImport), a.ImportMarkdownBook)
		api.GET("/tasks/:id", a.RequireAuth(), a.GetBackgroundJob)

		// ── 站内通知（notification:*；SSE 端点自行鉴权，EventSource 无法带请求头） ──
		notif := api.Group("/notifications", a.RequireAuth())
		{
			notif.GET("", a.RequirePermission(authz.NotificationRead), a.ListNotifications)
			notif.POST("/read", a.RequirePermission(authz.NotificationUpdate), a.MarkNotificationsRead)
		}
		api.POST("/stream-tickets", a.RequireAuth(), a.IssueStreamTicket)
		api.GET("/notifications/stream", a.SSENotifications)

		// ── 当前用户的协作邀请（未接受前不授予书籍访问权限） ──
		collaboration := api.Group("/collaboration", a.RequireAuth())
		{
			collaboration.GET("/invitations", a.RequirePermission(authz.CollaboratorRead), a.ListCollaborationInvitations)
			collaboration.POST("/invitations/:id/accept", a.RequirePermission(authz.CollaboratorUpdate), a.AcceptCollaborationInvitation)
			collaboration.POST("/invitations/:id/reject", a.RequirePermission(authz.CollaboratorUpdate), a.RejectCollaborationInvitation)
		}

		// ── 评论（comment:*） ──
		api.GET("/documents/:id/comments", a.OptionalAuth(), a.ListComments)
		api.POST("/documents/:id/comments", a.RequireAuth(), a.RequireEmailVerified(), a.RequirePermission(authz.CommentCreate), a.RateLimit(commentRateLimit), a.CreateComment)
		api.PUT("/comments/:id", a.RequireAuth(), a.RequirePermission(authz.CommentUpdate), a.UpdateComment)
		api.DELETE("/comments/:id", a.RequireAuth(), a.RequirePermission(authz.CommentDelete), a.DeleteComment)

		// ── 书籍评价（评分 + 评论，复用 comment:* 权限） ──
		api.GET("/books/:id/reviews", a.OptionalAuth(), a.ListBookReviews)
		api.POST("/books/:id/reviews", a.RequireAuth(), a.RequireEmailVerified(), a.RequirePermission(authz.CommentCreate), a.RateLimit(commentRateLimit), a.UpsertBookReview)
		api.DELETE("/reviews/:id", a.RequireAuth(), a.RequirePermission(authz.CommentDelete), a.DeleteBookReview)

		// ── 翻译（写作台调用后台配置的翻译方式） ──
		api.POST("/translate", a.RequireAuth(), a.RateLimit(commentRateLimit), a.Translate)

		// ── 内容举报（提交者不可查询举报人队列；管理端路由见 admin 组） ──
		api.POST("/reports", a.RequireAuth(), a.RequirePermission(authz.ReportCreate), a.RateLimit(reportRateLimit), a.CreateContentReport)

		// ── 点赞/收藏（reaction:*） ──
		books.POST("/:id/reactions", a.RequireAuth(), a.RequirePermission(authz.ReactionCreate), a.RateLimit(reactionRateLimit), a.PutReaction)
		books.DELETE("/:id/reactions", a.RequireAuth(), a.RequirePermission(authz.ReactionDelete), a.RateLimit(reactionRateLimit), a.DeleteReaction)
		books.GET("/:id/reactions/me", a.RequireAuth(), a.RequirePermission(authz.ReactionRead), a.MyBookReaction)
		api.GET("/users/me/reactions", a.RequireAuth(), a.RequirePermission(authz.ReactionRead), a.MyReactions)

		// ── 书籍关注等插件路由：由各插件子包（internal/plugins/<name>/）自行注册（迁移中） ──

		// 用户成长等级 /users/me/growth 等由 growth 插件子包自注册

		// ── 阅读进度（user 语义，读自己写自己） ──
		api.GET("/users/me/reading", a.RequireAuth(), a.RequirePermission(authz.ReadingProgressRead), a.MyReading)
		api.GET("/users/me/reading-stats", a.RequireAuth(), a.RequirePermission(authz.ReadingProgressRead), a.MyReadingStats)
		api.GET("/users/me/reading-activity", a.RequireAuth(), a.RequirePermission(authz.ReadingProgressRead), a.ReadingActivity)
		api.GET("/users/me/author-analytics", a.RequireAuth(), a.RequirePermission(authz.BookAnalyticsRead), a.MyAuthorAnalytics)
		api.GET("/users/me/reader-retention", a.RequireAuth(), a.RequirePermission(authz.BookAnalyticsRead), a.MyReaderRetention)
		api.GET("/users/me/export/books", a.RequireAuth(), a.RequirePermission(authz.BookExport), a.BatchExportMyBooks)
		api.GET("/users/me/exports", a.RequireAuth(), a.RequirePermission(authz.BookExport), a.MyExports)
		api.GET("/users/me/reading-goal", a.RequireAuth(), a.RequirePermission(authz.ReadingProgressRead), a.GetReadingGoal)
		api.PUT("/users/me/reading-goal", a.RequireAuth(), a.RequirePermission(authz.ReadingProgressUpdate), a.SaveReadingGoal)
		progress := api.Group("/reading-progress", a.RequireAuth())
		{
			progress.GET("/:bookId", a.RequirePermission(authz.ReadingProgressRead), a.GetReadingProgress)
			progress.PUT("/:bookId", a.RequirePermission(authz.ReadingProgressUpdate), a.SaveReadingProgress)
			progress.DELETE("/:bookId", a.RequirePermission(authz.ReadingProgressUpdate), a.ResetReadingProgress)
			progress.POST("/:bookId/complete", a.RequirePermission(authz.ReadingProgressUpdate), a.MarkBookRead)
		}

		// ── 私人阅读标注（annotation:*，始终按当前用户隔离） ──
		annotations := api.Group("", a.RequireAuth())
		{
			annotations.GET("/documents/:id/annotations", a.RequirePermission(authz.AnnotationRead), a.ListDocumentAnnotations)
			annotations.POST("/documents/:id/annotations", a.RequirePermission(authz.AnnotationCreate), a.CreateDocumentAnnotation)
			annotations.PUT("/annotations/:id", a.RequirePermission(authz.AnnotationUpdate), a.UpdateAnnotation)
			annotations.DELETE("/annotations/:id", a.RequirePermission(authz.AnnotationDelete), a.DeleteAnnotation)
			annotations.GET("/users/me/annotations", a.RequirePermission(authz.AnnotationRead), a.ListMyAnnotations)
			annotations.GET("/users/me/annotations/export", a.RequirePermission(authz.AnnotationRead), a.ExportMyAnnotations)
		}

		// ── 成就（本人完整进度；公开陈列见 public 组） ──

		// ── 上传 ──
		api.POST("/upload", a.RequireAuth(), a.RequireEmailVerified(), a.RequirePermission(authz.UploadCreate), a.RateLimit(uploadRateLimit), a.Upload)

		// ── 标签管理（tag:*）：POST /tags、DELETE /tags/:id 由 tags 插件子包自注册 ──

		// ── 站点与系统管理（仅管理员） ──
		admin := api.Group("", a.RequireAuth(), a.RequireAdmin())
		{
			admin.PUT("/site", a.RequirePermission(authz.SiteUpdate), a.UpdateSiteConfig)
			admin.GET("/rate-limits", a.RequirePermission(authz.SiteUpdate), a.AdminGetRateLimits)
			admin.PUT("/rate-limits", a.RequirePermission(authz.SiteUpdate), a.AdminUpdateRateLimits)
			admin.GET("/oauth", a.RequirePermission(authz.SiteUpdate), a.AdminGetOAuth)
			admin.PUT("/oauth", a.RequirePermission(authz.SiteUpdate), a.AdminSaveOAuth)
			admin.GET("/logs", a.RequirePermission(authz.SiteUpdate), a.AdminGetLogConfig)
			admin.PUT("/logs", a.RequirePermission(authz.SiteUpdate), a.AdminUpdateLogConfig)
			admin.GET("/mail", a.RequirePermission(authz.SiteUpdate), a.AdminGetMail)
			admin.PUT("/mail", a.RequirePermission(authz.SiteUpdate), a.AdminSaveMail)
			admin.GET("/translation", a.RequirePermission(authz.SiteUpdate), a.AdminGetTranslation)
			admin.PUT("/translation", a.RequirePermission(authz.SiteUpdate), a.AdminSaveTranslation)
			admin.GET("/storage", a.RequirePermission(authz.SiteUpdate), a.AdminGetStorage)
			admin.PUT("/storage", a.RequirePermission(authz.SiteUpdate), a.AdminSaveStorage)
			admin.GET("/registration", a.RequirePermission(authz.SiteUpdate), a.GetRegistrationSettings)
			admin.PUT("/registration", a.RequirePermission(authz.SiteUpdate), a.UpdateRegistrationSettings)
			admin.GET("/captcha-settings", a.RequirePermission(authz.SiteUpdate), a.GetCaptchaSettings)
			admin.PUT("/captcha-settings", a.RequirePermission(authz.SiteUpdate), a.UpdateCaptchaSettings)
			admin.GET("/login-security", a.RequirePermission(authz.SiteUpdate), a.GetLoginSecurity)
			admin.PUT("/login-security", a.RequirePermission(authz.SiteUpdate), a.UpdateLoginSecurity)
			admin.GET("/content-settings", a.RequirePermission(authz.SiteUpdate), a.GetContentSettings)
			admin.PUT("/content-settings", a.RequirePermission(authz.SiteUpdate), a.UpdateContentSettings)
			admin.GET("/system/version", a.RequirePermission(authz.SystemRead), a.SystemVersion)
			admin.GET("/system/cluster", a.RequirePermission(authz.SystemRead), a.AdminClusterStatus)
			admin.POST("/system/upgrade", a.RequirePermission(authz.SystemUpgrade), a.SystemUpgrade)

			// 用户管理（user:manage，仅管理员）。独立 /admin 前缀与公开 /users/:username 区分
			admin.GET("/admin/users", a.RequirePermission(authz.UserManage), a.AdminListUsers)
			admin.PUT("/admin/users/:id/role", a.RequirePermission(authz.UserManage), a.AdminUpdateUserRole)
			admin.PUT("/admin/users/:id/status", a.RequirePermission(authz.UserManage), a.AdminUpdateUserStatus)
			admin.DELETE("/admin/users/:id", a.RequirePermission(authz.UserManage), a.AdminDeleteUser)
			// 书籍管理：列出全站所有可见性与状态的书籍；更新与删除复用 book:* 对象权限端点
			admin.GET("/admin/books", a.RequirePermission(authz.BookRead), a.AdminListBooks)
			// 章节管理：只列元数据；更新与删除复用 document:* 对象权限端点
			admin.GET("/admin/documents", a.RequirePermission(authz.DocumentRead), a.AdminListDocuments)
			// 控制台首页时间线（user:manage，仅管理员）：最近注册用户 + 最近建书（不限可见性）
			admin.GET("/admin/activity", a.RequirePermission(authz.UserManage), a.AdminActivity)
			admin.GET("/admin/stats", a.RequirePermission(authz.StatsRead), a.AdminStats)
			admin.GET("/admin/audit-logs/facets", a.RequirePermission(authz.AuditRead), a.AdminAuditLogFacets)
			admin.GET("/admin/audit-logs", a.RequirePermission(authz.AuditRead), a.AdminListAuditLogs)
			admin.GET("/admin/tasks/types", a.RequirePermission(authz.TaskRead), a.AdminBackgroundJobTypes)
			admin.GET("/admin/tasks", a.RequirePermission(authz.TaskRead), a.AdminListBackgroundJobs)
			admin.POST("/admin/tasks/:id/retry", a.RequirePermission(authz.TaskRetry), a.AdminRetryBackgroundJob)
			admin.GET("/admin/reports", a.RequirePermission(authz.ReportRead), a.AdminListContentReports)
			admin.PUT("/admin/reports/:id", a.RequirePermission(authz.ReportUpdate), a.AdminResolveContentReport)

			// 后台标签管理 /admin/tags* 由 tags 插件子包自注册（自带 RequireAdmin + 特性插件守卫 + tag 权限）

			// AI 服务（大模型）：站点级配置，插件经 Core.AIChat/AIEmbed 调用
			admin.GET("/admin/ai", a.RequirePermission(authz.SiteUpdate), a.AdminGetAI)
			admin.PUT("/admin/ai", a.RequirePermission(authz.SiteUpdate), a.AdminUpdateAI)
			admin.POST("/admin/ai/test", a.RequirePermission(authz.SiteUpdate), a.AdminTestAI)
			admin.GET("/admin/ai/usage", a.RequirePermission(authz.SiteUpdate), a.AdminAIUsage)
			admin.GET("/admin/ai/usage/logs", a.RequirePermission(authz.SiteUpdate), a.AdminAIUsageLogs)
			admin.GET("/admin/ai/alerts", a.RequirePermission(authz.SiteUpdate), a.AdminAIAlerts)
			admin.GET("/admin/ai/usage/export", a.RequirePermission(authz.SiteUpdate), a.AdminExportAIUsage)
			// 通用系统配置（config:manage，仅管理员）：任意 key-value 配置的增删改查
			// 权益：基础值（全站默认）；等级/会员等来源由插件提供
			admin.GET("/admin/entitlements", a.RequirePermission(authz.SiteUpdate), a.AdminEntitlements)
			admin.PUT("/admin/entitlements/base", a.RequirePermission(authz.SiteUpdate), a.AdminUpdateEntitlementBase)
			admin.GET("/admin/configs", a.RequirePermission(authz.ConfigManage), a.AdminListConfigs)
			admin.PUT("/admin/configs", a.RequirePermission(authz.ConfigManage), a.AdminUpsertConfig)
			admin.DELETE("/admin/configs/:key", a.RequirePermission(authz.ConfigManage), a.AdminDeleteConfig)

			// 插件管理（plugin:manage，仅管理员）：安装/卸载 PDF 导出等后台插件
			admin.GET("/admin/plugins", a.RequirePermission(authz.PluginManage), a.AdminListPlugins)
			admin.POST("/admin/plugins/:key/install", a.RequirePermission(authz.PluginManage), a.AdminInstallPlugin)
			admin.POST("/admin/plugins/:key/uninstall", a.RequirePermission(authz.PluginManage), a.AdminUninstallPlugin)

			// 成长等级管理 /admin/growth/* 由 growth 插件子包自注册（自带 RequireAdmin + 特性插件守卫 + 权限）
		}

		// 插件操作日志 SSE（自行按 query token 鉴权，EventSource 无法带请求头）
		api.GET("/admin/plugins/:key/logs", a.AdminPluginLogs)
	}

	// 插件路由：各插件子包（internal/plugins/<name>/）自注册的行为在此统一挂载到 /api/v1。
	for _, p := range plugincore.Behaviors() {
		p.RegisterRoutes(api, a)
	}

	RegisterWeb(r, a.web)
	return r
}
