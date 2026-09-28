package qa

import (
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// AI 调用记录的关联：问答相关的调用跳转到书籍的「问答」；读者问题分析跳转到书籍设置中的「读者问题」。
func init() {
	plugincore.RegisterAIUsageRef("qa.", func(core plugincore.Core, viewer *models.User, in plugincore.AIUsageRefInput) *plugincore.AIUsageRef {
		if in.RefType != "book" {
			return nil
		}
		book := plugincore.ReadableBook(core, viewer, in.RefID)
		if book == nil {
			return nil
		}
		if in.Feature == "qa.insights" && (core.IsAdmin(viewer) || core.CanEditBookContent(viewer, book)) {
			return &plugincore.AIUsageRef{Kind: "qa", Title: book.Title, Link: "/book/settings/" + book.Slug + "/reader-questions"}
		}
		return &plugincore.AIUsageRef{Kind: "qa", Title: book.Title, Link: "/book/detail/" + book.Slug + "?tab=qa"}
	})
}
