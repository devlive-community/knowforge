package aiwriter

import (
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// AI 调用记录的关联：AI 写作的调用跳转到该书的写作台（可编辑者）。
func init() {
	plugincore.RegisterAIUsageRef("aiwriter.", func(core plugincore.Core, viewer *models.User, in plugincore.AIUsageRefInput) *plugincore.AIUsageRef {
		if in.RefType != "book" {
			return nil
		}
		book := plugincore.ReadableBook(core, viewer, in.RefID)
		if book == nil || (!core.IsAdmin(viewer) && !core.CanEditBookContent(viewer, book)) {
			return nil
		}
		return &plugincore.AIUsageRef{Kind: "book", Title: book.Title, Link: "/book/writer/" + book.Slug}
	})
}
