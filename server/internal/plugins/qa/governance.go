package qa

import (
	"strconv"
	"time"

	"gorm.io/gorm"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// 社区问答的内容治理：提问与回答是公开的用户内容，登记为 plugincore.UserContent，
// 发布前交发布守卫审查（如敏感词审核插件），并可被举报、下架。本插件不认识具体的审核插件。
//
// 可见性：""（公开）| held（待审核）| hidden（驳回或下架）。新内容先以 held 写入、审查放行后再公开，
// 未经审查的内容不会有任何对外可见的窗口；held/hidden 只对本人与管理员可见。
// 被拦截内容的通知（向作者提问、向提问者回答）延后到审核通过时发送。

const (
	kindQuestion = "qa_question"
	kindAnswer   = "qa_answer"
	visPublic    = ""
	visHeld      = "held"
	visHidden    = "hidden"
)

func init() {
	plugincore.RegisterUserContent(plugincore.UserContent{Kind: kindQuestion, Resolve: resolveQuestion, SetVisible: setQuestionVisible})
	plugincore.RegisterUserContent(plugincore.UserContent{Kind: kindAnswer, Resolve: resolveAnswer, SetVisible: setAnswerVisible})
}

func questionLink(book *models.Book, questionID uint) string {
	return "/book/detail/" + book.Slug + "?tab=qa&question=" + strconv.FormatUint(uint64(questionID), 10)
}

// canSee 非公开内容只对本人与管理员可见。
func canSee(core plugincore.Core, viewer *models.User, ownerID uint, visibility string) bool {
	return visibility == visPublic || (viewer != nil && (viewer.ID == ownerID || core.IsAdmin(viewer)))
}

func resolveQuestion(core plugincore.Core, viewer *models.User, id uint) (plugincore.ContentRef, bool) {
	var q Question
	var book models.Book
	db := core.Gorm()
	if !core.PluginEnabled(plugins.KeyQA) || db.First(&q, id).Error != nil || db.First(&book, q.BookID).Error != nil {
		return plugincore.ContentRef{}, false
	}
	if viewer != nil && (!core.CanReadBook(viewer, &book) || !canSee(core, viewer, q.UserID, q.Visibility)) {
		return plugincore.ContentRef{}, false
	}
	return plugincore.ContentRef{Label: q.Title, Link: questionLink(&book, q.ID), OwnerID: q.UserID, BookID: book.ID}, true
}

func resolveAnswer(core plugincore.Core, viewer *models.User, id uint) (plugincore.ContentRef, bool) {
	var a Answer
	var q Question
	var book models.Book
	db := core.Gorm()
	if !core.PluginEnabled(plugins.KeyQA) || db.First(&a, id).Error != nil || db.First(&q, a.QuestionID).Error != nil || db.First(&book, q.BookID).Error != nil {
		return plugincore.ContentRef{}, false
	}
	if viewer != nil && (!core.CanReadBook(viewer, &book) || !canSee(core, viewer, q.UserID, q.Visibility) || !canSee(core, viewer, a.UserID, a.Visibility)) {
		return plugincore.ContentRef{}, false
	}
	return plugincore.ContentRef{Label: truncate(a.Body, 80), Link: questionLink(&book, q.ID), OwnerID: a.UserID, BookID: book.ID}, true
}

// setQuestionVisible 审核通过/恢复或驳回/下架提问；待审核的提问首次公开时补发给作者的提问通知。
func setQuestionVisible(core plugincore.Core, id uint, visible bool) error {
	db := core.Gorm()
	var q Question
	if err := db.First(&q, id).Error; err != nil {
		return err
	}
	target := visHidden
	if visible {
		target = visPublic
	}
	if q.Visibility == target {
		return nil
	}
	if err := db.Model(&Question{}).Where("id = ?", q.ID).Update("visibility", target).Error; err != nil {
		return err
	}
	if visible && q.Visibility == visHeld {
		notifyAsked(core, &q)
	}
	return nil
}

// setAnswerVisible 审核通过/恢复或驳回/下架回答：回答数只统计公开的回答；下架被采纳的回答时问题回到待解决。
func setAnswerVisible(core plugincore.Core, id uint, visible bool) error {
	db := core.Gorm()
	var a Answer
	if err := db.First(&a, id).Error; err != nil {
		return err
	}
	target := visHidden
	if visible {
		target = visPublic
	}
	if a.Visibility == target {
		return nil
	}
	wasPublic := a.Visibility == visPublic
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Answer{}).Where("id = ?", a.ID).Update("visibility", target).Error; err != nil {
			return err
		}
		updates := map[string]any{"updated_at": time.Now()}
		switch {
		case visible && !wasPublic:
			updates["answer_count"] = gorm.Expr("answer_count + 1")
		case !visible && wasPublic:
			updates["answer_count"] = gorm.Expr("CASE WHEN answer_count > 0 THEN answer_count - 1 ELSE 0 END")
		}
		if err := tx.Model(&Question{}).Where("id = ?", a.QuestionID).Updates(updates).Error; err != nil {
			return err
		}
		if !visible {
			return tx.Model(&Question{}).Where("id = ? AND accepted_answer_id = ?", a.QuestionID, a.ID).
				Updates(map[string]any{"accepted_answer_id": 0, "status": "open"}).Error
		}
		return nil
	})
	if err != nil {
		return err
	}
	if visible && a.Visibility == visHeld {
		notifyAnswered(core, &a)
	}
	return nil
}

func notifyAsked(core plugincore.Core, q *Question) {
	var book models.Book
	if core.Gorm().First(&book, q.BookID).Error != nil || book.UserID == q.UserID {
		return
	}
	core.NotifyI18n(book.UserID, "comment", "notify.qa.asked", map[string]string{"book": book.Title, "title": q.Title},
		map[string]any{"link": questionLink(&book, q.ID)})
	// 书籍收到公开提问：供 Webhook 等订阅方转发（UserID 为作者）
	id := strconv.FormatUint(uint64(q.ID), 10)
	body := []rune(q.Body)
	if len(body) > 500 {
		body = append(body[:500], '…')
	}
	var asker models.User
	core.Gorm().Select("id", "username", "nickname").First(&asker, q.UserID)
	plugincore.FireActivity(core, plugincore.ActivityEvent{UserID: book.UserID, Type: "qa.question_received", SourceType: "qa_question", SourceID: id,
		DedupeKey: "qa.question_received:" + id, Data: map[string]any{"book_id": book.ID, "doc_id": q.DocID, "question": map[string]any{
			"id": q.ID, "title": q.Title, "body": string(body), "link": questionLink(&book, q.ID), "created_at": q.CreatedAt,
			"author": map[string]any{"id": asker.ID, "username": asker.Username, "nickname": asker.Nickname}}}})
}

func notifyAnswered(core plugincore.Core, a *Answer) {
	var q Question
	var book models.Book
	db := core.Gorm()
	if db.First(&q, a.QuestionID).Error != nil || db.First(&book, q.BookID).Error != nil || q.UserID == a.UserID {
		return
	}
	core.NotifyI18n(q.UserID, "comment", "notify.qa.answered", map[string]string{"title": q.Title}, map[string]any{"link": questionLink(&book, q.ID)})
}

// publishOrHold 新内容（已以 held 写入）交发布守卫审查：放行则公开并返回 false；拦截则保持待审核，返回 true 与说明。
// 须在事务之外调用（守卫可能写库）。
func publishOrHold(core plugincore.Core, t plugincore.PublishTarget) (bool, string) {
	if v := plugincore.CheckPublish(core, t); v.Hold {
		return true, v.Message
	}
	uc, _ := plugincore.UserContentFor(t.Kind)
	if err := uc.SetVisible(core, t.ID, true); err != nil {
		return true, "发布失败，请稍后重试"
	}
	return false, ""
}
