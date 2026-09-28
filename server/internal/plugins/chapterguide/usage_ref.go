package chapterguide

import (
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// AI 调用记录的关联：生成导读的调用跳转到书籍设置中的「章节导读」（可编辑者），否则回退为书籍/章节。
func init() {
	plugincore.RegisterAIUsageRef("chapterguide.", func(core plugincore.Core, viewer *models.User, in plugincore.AIUsageRefInput) *plugincore.AIUsageRef {
		bookID := in.RefID
		if in.RefType == "document" {
			var doc models.Document
			if core.Gorm().Select("id", "book_id").First(&doc, in.RefID).Error != nil {
				return nil
			}
			bookID = doc.BookID
		}
		book := plugincore.ReadableBook(core, viewer, bookID)
		if book == nil {
			return nil
		}
		if core.IsAdmin(viewer) || core.CanEditBookContent(viewer, book) {
			return &plugincore.AIUsageRef{Kind: "book", Title: book.Title, Link: "/book/settings/" + book.Slug + "/chapter-guides"}
		}
		return &plugincore.AIUsageRef{Kind: "book", Title: book.Title, Link: "/book/detail/" + book.Slug}
	})
}
