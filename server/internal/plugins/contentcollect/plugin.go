// Package contentcollect 内容采集插件：单页网页采集（导入成书/章节、编辑器插入正文）与整站采集
// （目录预览 → 后台 worker 抓取 → 失败重试 → 采集历史），以及基于采集记录的站内链接改写。
// 通过 plugincore.Core 访问核心能力，不依赖 app 包。
package contentcollect

import (
	"context"
	"encoding/json"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/i18ntext"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// behavior 插件行为：路由、后台任务与书籍装饰。core 在 RegisterRoutes 时注入。
type behavior struct{ core plugincore.Core }

var instance = &behavior{}

func init() {
	i18ntext.Register("notify.collect.finished", map[string]string{"zh-CN": "《{book}》采集完成", "en": `Finished collecting "{book}"`})
	plugincore.RegisterBehavior(instance)
	// 整站采集 worker：任务队列由核心在每次创建时注册（队列可能在安装向导完成后才创建）
	plugincore.RegisterJob(siteCrawlJobType, func(core plugincore.Core) func(context.Context, json.RawMessage) error {
		return (&behavior{core: core}).runSiteCrawlJob
	})
	// 书籍列表/详情回填「采集中」标记
	plugincore.OnDecorateBooks(func(core plugincore.Core, books []*models.Book) {
		(&behavior{core: core}).attachCrawlingFlags(books)
	})
	plugins.Register(plugins.Meta{
		Order:       70,
		Key:         plugins.KeyContentCollect,
		Name:        "内容采集",
		Description: "网页采集与整站采集：把外部网页/文档站点抓取为书籍章节。含编辑器「采集网页」、网页导入成书、整站递归采集（目录预览 + 内容区确认 + 后台采集 + 失败重试 + 采集历史）。禁用后所有采集入口与接口一并停用（默认启用）。首次启用建表、注册权限，卸载可清除采集记录。",
		Kind:        plugins.KindFeature,
		Builtin:     true,
		Models:      []any{&CrawlJob{}, &CrawlPage{}},
		Tables:      []string{"crawl_pages", "crawl_jobs"},
		AdminPerms:  []authz.Permission{authz.CollectManage},
		UserPerms:   []authz.Permission{authz.CollectRead, authz.CollectCreate, authz.CollectManage},
	})
}

func (cc *behavior) Key() string { return plugins.KeyContentCollect }

// RegisterRoutes 挂载采集相关路由（路径、中间件与迁移前一致；站内链接改写额外受本插件启用守卫）。
func (cc *behavior) RegisterRoutes(api *gin.RouterGroup, core plugincore.Core) {
	cc.core = core
	auth := core.RequireAuth()
	feature := core.RequireFeaturePlugin(plugins.KeyContentCollect)

	// 单页网页采集
	api.POST("/books/:id/documents/import-web", auth, cc.requireCollect(entCollectPage), core.RequirePermission(authz.DocumentCreate), cc.ImportWebDocument)
	api.POST("/import/web", auth, cc.requireCollect(entCollectPage), core.RequirePermission(authz.BookImport), cc.ImportWebBook)
	// 采集网页正文为 Markdown（不建文档），供写作编辑器插入
	api.POST("/import/web-content", auth, cc.requireCollect(entCollectPage), core.RequirePermission(authz.DocumentCreate), cc.CollectWebContent)
	// 浏览器渲染是否可用（依赖无头浏览器插件），供前端联动禁用「浏览器运行 JavaScript」采集模式
	api.GET("/import/browser-available", auth, feature, core.RequirePermission(authz.BookImport), cc.BrowserRenderAvailable)

	// 整站采集（collect:* 权限）
	collect := api.Group("/collect", auth, feature)
	collect.POST("/site/preview", cc.requireCollect(entCollectSite), core.RequirePermission(authz.CollectCreate), cc.SiteCrawlPreview)
	collect.POST("/site", cc.requireCollect(entCollectSite), core.RequirePermission(authz.CollectCreate), cc.StartSiteCrawl)
	collect.GET("/jobs/:id", core.RequirePermission(authz.CollectRead), cc.GetCrawlJob)
	collect.POST("/jobs/:id/retry", core.RequirePermission(authz.CollectManage), cc.RetryCrawlJob)
	collect.POST("/pages/:id/retry", core.RequirePermission(authz.CollectManage), cc.RetryCrawlPage)
	api.GET("/books/:id/collect/jobs", auth, feature, core.RequirePermission(authz.CollectRead), cc.ListBookCrawlJobs)

	// 采集规则（管理员）：内置规则开关、自定义规则与试采预览
	admin := []gin.HandlerFunc{auth, core.RequireAdmin(), feature, core.RequirePermissionMiddleware(authz.CollectManage)}
	api.GET("/admin/collect/rules", append(admin, cc.AdminGetRules)...)
	api.PUT("/admin/collect/rules", append(admin, cc.AdminUpdateRules)...)
	api.POST("/admin/collect/rules/preview", append(admin, cc.AdminPreviewRules)...)

	// 书籍清理：按采集记录把外链改写为站内链接
	api.POST("/books/:id/cleanup/internal-links", auth, feature, core.RequirePermission(authz.BookUpdate), cc.CleanupBookInternalLinks)
}
