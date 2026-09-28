// Package embed 嵌入组件插件：书籍目录与单个章节可以 iframe 嵌入到其他网站（页面见前端 /embed/*）。
// 嵌入页按游客身份渲染（付费章节只显示试读），只有 /embed/* 允许被其他网站嵌入，站点其余页面只允许同源嵌入。
package embed

import "knowforge/server/internal/plugins"

func init() {
	plugins.Register(plugins.Meta{
		Order:       130,
		Key:         plugins.KeyEmbed,
		Name:        "嵌入组件",
		Description: "作者可生成 iframe 代码，把书籍目录或单个章节嵌入到其他网站（博客、文档站等）；嵌入页按游客身份显示，付费章节只显示试读，并链接回本站阅读。默认关闭。",
		Kind:        plugins.KindFeature,
		Builtin:     true,
		EnabledKey:  "embed_enabled",
	})
}
