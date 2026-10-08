package app

import (
	"fmt"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// 发布守卫的核心接入（见 plugincore.PublishGuard）：章节发布/已发布章节改动、书籍公开前交给插件审查。
// 注意：守卫实现可能写库，调用必须在事务之外（SQLite 单写者）。

func documentPublishTarget(book *models.Book, doc *models.Document, actorID uint) plugincore.PublishTarget {
	return plugincore.PublishTarget{
		Kind: plugincore.PublishDocument, ID: doc.ID, BookID: book.ID, UserID: doc.UserID, Title: doc.Title, ActorID: actorID,
		Fields:    map[string]string{"title": doc.Title, "content": doc.Content},
		Requested: map[string]string{"status": "published"},
	}
}

func bookPublishTarget(book *models.Book, actorID uint) plugincore.PublishTarget {
	return plugincore.PublishTarget{
		Kind: plugincore.PublishBook, ID: book.ID, BookID: book.ID, UserID: book.UserID, Title: book.Title, ActorID: actorID,
		Fields:    map[string]string{"title": book.Title, "description": book.Description},
		Requested: map[string]string{"is_public": "true", "status": book.Status},
	}
}

func bookVisible(b *models.Book) bool { return b.IsPublic && isPubliclyReadableBookStatus(b.Status) }

// tryPublishDocument 草稿章节尝试发布：审查通过则置为已发布；返回拦截说明（放行为空）。
func (a *App) tryPublishDocument(book *models.Book, doc *models.Document, actorID uint) string {
	if v := plugincore.CheckPublish(a, documentPublishTarget(book, doc, actorID)); v.Hold {
		return v.Message
	}
	doc.Status = "published"
	a.DB.Model(&models.Document{}).Where("id = ?", doc.ID).Update("status", "published")
	return ""
}

// GuardDocumentPublish 已写入且状态为已发布的新章节（导入、采集等）：审查被拦截时改回草稿；返回拦截说明。
func (a *App) GuardDocumentPublish(book *models.Book, doc *models.Document, actorID uint) string {
	if doc.Status != "published" {
		return ""
	}
	if v := plugincore.CheckPublish(a, documentPublishTarget(book, doc, actorID)); v.Hold {
		doc.Status = "draft"
		a.DB.Model(&models.Document{}).Where("id = ?", doc.ID).Update("status", "draft")
		return v.Message
	}
	return ""
}

// guardBookVisible 已写入且对外可见的书籍：审查被拦截时改回私有；返回拦截说明。
func (a *App) guardBookVisible(book *models.Book, actorID uint) string {
	if !bookVisible(book) {
		return ""
	}
	if v := plugincore.CheckPublish(a, bookPublishTarget(book, actorID)); v.Hold {
		book.IsPublic = false
		a.DB.Model(&models.Book{}).Where("id = ?", book.ID).Update("is_public", false)
		return v.Message
	}
	a.notifyBookIndexableChange(nil, book)
	return ""
}

// ApplyModeration 审核结论落地（绕过发布守卫）：approve 应用 requested（发布章节 / 公开书籍）；否则撤回发布（章节→草稿、书籍→私有）。
func (a *App) ApplyModeration(kind string, id uint, approve bool, requested map[string]string) error {
	switch kind {
	case plugincore.PublishDocument:
		var doc models.Document
		if err := a.DB.First(&doc, id).Error; err != nil {
			return fmt.Errorf("章节不存在")
		}
		var book models.Book
		if err := a.DB.First(&book, doc.BookID).Error; err != nil {
			return fmt.Errorf("书籍不存在")
		}
		oldBook, oldDoc := book, doc
		if !approve {
			if err := a.DB.Model(&models.Document{}).Where("id = ?", doc.ID).Update("status", "draft").Error; err != nil {
				return err
			}
			doc.Status = "draft"
			a.notifyDocumentIndexableChange(&oldBook, &book, &oldDoc, &doc)
			return nil
		}
		oldStatus := doc.Status
		if err := a.DB.Model(&models.Document{}).Where("id = ?", doc.ID).Update("status", "published").Error; err != nil {
			return err
		}
		doc.Status = "published"
		if book.Status == "draft" { // 与编辑器发布一致：草稿书有章节发布后提升为「连载中」
			if err := a.DB.Model(&models.Book{}).Where("id = ?", book.ID).Update("status", "in_progress").Error; err != nil {
				return err
			}
			book.Status = "in_progress"
		}
		if oldStatus != "published" {
			plugincore.FireChapterPublished(a, &book, &doc)
		}
		a.notifyDocumentIndexableChange(&oldBook, &book, &oldDoc, &doc)
		return nil
	case plugincore.PublishBook:
		var book models.Book
		if err := a.DB.First(&book, id).Error; err != nil {
			return fmt.Errorf("书籍不存在")
		}
		oldBook := book
		if !approve {
			if err := a.DB.Model(&models.Book{}).Where("id = ?", book.ID).Update("is_public", false).Error; err != nil {
				return err
			}
			book.IsPublic = false
			a.notifyBookIndexableChange(&oldBook, &book)
			return nil
		}
		updates := map[string]any{"is_public": requested["is_public"] != "false"}
		if s := requested["status"]; bookStatuses[s] {
			updates["status"] = s
			book.Status = s
		}
		book.IsPublic = updates["is_public"].(bool)
		if err := a.DB.Model(&models.Book{}).Where("id = ?", book.ID).Updates(updates).Error; err != nil {
			return err
		}
		a.notifyBookIndexableChange(&oldBook, &book)
		return nil
	}
	// 插件登记的用户内容（如问答的提问、回答）：由登记者落地
	if uc, found := plugincore.UserContentFor(kind); found {
		return uc.SetVisible(a, id, approve)
	}
	return fmt.Errorf("不支持的审核对象：%s", kind)
}
