package app

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

// RegisterWeb 将页面请求转发给由 Go 托管的内嵌 Next.js；普通开发构建
// 没有内嵌资源时仍返回说明页，方便前后端分开调试。
var indexNowVerificationPath = regexp.MustCompile(`^/([a-f0-9]{32})\.txt$`)

func RegisterWeb(r *gin.Engine, web *webRuntime, indexNowVerificationHandler ...gin.HandlerFunc) {
	r.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path
		if match := indexNowVerificationPath.FindStringSubmatch(p); len(match) == 2 && len(indexNowVerificationHandler) > 0 {
			c.Set("indexnow_verification_key", match[1])
			indexNowVerificationHandler[0](c)
			return
		}
		if !strings.HasPrefix(p, "/api/") && !strings.HasPrefix(p, "/uploads/") {
			if web != nil {
				web.proxy.ServeHTTP(c.Writer, c.Request)
				return
			}
			c.Data(http.StatusNotFound, "text/html; charset=utf-8", []byte(placeholderPage))
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "接口不存在"})
	})
}

const placeholderPage = `<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>KnowForge</title></head>
<body style="font-family:sans-serif;padding:40px;text-align:center;color:#475569;">
<h1>KnowForge</h1>
<p>当前二进制未内嵌 Web 运行时，仅提供 API 服务。</p>
<p style="color:#94a3b8">开发时请单独运行 Next.js；发布构建会把 Web 运行时嵌入同一二进制。</p>
</body></html>
`
