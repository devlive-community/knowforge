package tags

import (
	"knowforge/server/internal/authz"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

func init() {
	plugincore.RegisterURLColumns("tags", "icon_value") // 存储迁移：标签图标
	plugins.Register(plugins.Meta{
		Order:       80,
		Key:         plugins.KeyTags,
		Name:        "标签系统",
		Description: "书籍标签浏览、按标签检索与后台标签管理（图标）。禁用后标签页面与相关接口一并停用（默认启用）。首次启用建表、注册权限，卸载可清除数据。",
		Kind:        plugins.KindFeature,
		Builtin:     true,
		Models:      []any{&models.Tag{}, &models.BookTag{}},
		Tables:      []string{"book_tags", "tags"},
		AdminPerms:  []authz.Permission{authz.TagDelete, authz.TagManage},
		UserPerms:   []authz.Permission{authz.TagRead, authz.TagCreate},
	})
}
