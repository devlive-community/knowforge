package readaloud

import (
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// AI 调用记录的关联：朗读的合成调用跳转到对应章节的阅读页。
func init() {
	plugincore.RegisterAIUsageRef("readaloud.", func(core plugincore.Core, viewer *models.User, in plugincore.AIUsageRefInput) *plugincore.AIUsageRef {
		if in.RefType != "document" {
			return nil
		}
		var doc models.Document
		if core.Gorm().Select("id", "book_id", "slug", "title").First(&doc, in.RefID).Error != nil {
			return nil
		}
		book := plugincore.ReadableBook(core, viewer, doc.BookID)
		if book == nil {
			return nil
		}
		return &plugincore.AIUsageRef{Kind: "document", Title: book.Title + " · " + doc.Title, Link: "/book/reader/" + book.Slug + "/" + doc.Slug}
	})
}
