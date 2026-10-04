package app

// 触发各内置插件子包的 init() 自注册（plugins.Register）。app 只需 blank import，
// 之后通过 plugins.All() 读取清单；新增插件时在此追加一行 blank import 即可被发现。
import (
	_ "knowforge/server/internal/plugins/achievements"
	_ "knowforge/server/internal/plugins/aiwriter"
	_ "knowforge/server/internal/plugins/backlinks"
	_ "knowforge/server/internal/plugins/bookfollow"
	_ "knowforge/server/internal/plugins/booklists"
	_ "knowforge/server/internal/plugins/booktranslations"
	_ "knowforge/server/internal/plugins/bookversions"
	_ "knowforge/server/internal/plugins/chapterguide"
	_ "knowforge/server/internal/plugins/contentcollect"
	_ "knowforge/server/internal/plugins/embed"
	_ "knowforge/server/internal/plugins/feeds"
	_ "knowforge/server/internal/plugins/growth"
	_ "knowforge/server/internal/plugins/membership"
	_ "knowforge/server/internal/plugins/moderation"
	_ "knowforge/server/internal/plugins/paidcontent"
	_ "knowforge/server/internal/plugins/payment"
	_ "knowforge/server/internal/plugins/pdfexport"
	_ "knowforge/server/internal/plugins/qa"
	_ "knowforge/server/internal/plugins/readaloud"
	_ "knowforge/server/internal/plugins/tags"
	_ "knowforge/server/internal/plugins/teams"
	_ "knowforge/server/internal/plugins/templates"
	_ "knowforge/server/internal/plugins/watermark"
	_ "knowforge/server/internal/plugins/webhooks"
)
