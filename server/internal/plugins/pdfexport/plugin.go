// Package pdfexport 无头浏览器（Chromium）运行时插件：书籍 PDF 导出端点（渲染内嵌 Web 的打印页并输出 PDF）。
// 运行时的下载/安装由核心插件管理负责（内容采集的浏览器渲染同样依赖它）。
package pdfexport

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// 每月 PDF 导出次数为权益（生成 PDF 需启动无头浏览器，开销较大）；基础为不限，可作为会员权益。
const (
	entPDFMonthly = "export.pdf_monthly"
	cfgPDFMonthly = "entitlement_export_pdf_monthly"
)

type behavior struct{ core plugincore.Core }

func init() {
	plugincore.RegisterBehavior(&behavior{})
	plugins.Register(plugins.Meta{
		Order:       10,
		Key:         plugins.KeyPDFExport,
		Name:        "无头浏览器 (Chromium)",
		Description: "安装官方 chrome-headless-shell，用于书籍 PDF 导出与网页浏览器渲染采集（运行 JavaScript）。约 130–170MB，下载到数据目录。",
		SizeHint:    "~150MB",
		Kind:        plugins.KindRuntime,
		Builtin:     false, // 需从外部下载运行时，归为「外部插件」
	})
	plugincore.RegisterEntitlement(plugincore.EntitlementDef{
		Key: entPDFMonthly, Kind: plugincore.EntitlementLimit, Unit: "exports", Min: 0, Max: 100000, AllowUnlimited: true, Order: 35,
		Available: func(core plugincore.Core) bool { return core.InstalledChromePath() != "" },
		Base: func(core plugincore.Core) int64 {
			if v, err := strconv.ParseInt(core.GetSetting(cfgPDFMonthly), 10, 64); err == nil && (v == plugincore.Unlimited || (v >= 0 && v <= 100000)) {
				return v
			}
			return plugincore.Unlimited
		},
		SetBase: func(core plugincore.Core, v int64) error {
			return core.SetSetting(cfgPDFMonthly, strconv.FormatInt(v, 10), "权益：每月 PDF 导出次数（基础）")
		},
	})
}

func (px *behavior) Key() string { return plugins.KeyPDFExport }

// RegisterRoutes 挂载 PDF 导出（公开路由，可匿名导出公开书籍；未安装运行时由处理器返回明确提示）。
func (px *behavior) RegisterRoutes(api *gin.RouterGroup, core plugincore.Core) {
	px.core = core
	api.GET("/books/:id/export/pdf", core.OptionalAuth(), px.ExportBookPDF)
}
