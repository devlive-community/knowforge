package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/i18ntext"
	"knowforge/server/internal/models"
)

// —— 章节分工：每章可指定负责人（书籍作者或协作者）、写作阶段（未开始 / 写作中 / 待审阅 / 已定稿）与截止日期。
// 能编辑的人可设置；分配给某人时通知对方，章节进入「待审阅」时通知书籍作者与编辑者。——

var writerTaskStages = map[string]bool{"todo": true, "writing": true, "review": true, "done": true}

func init() {
	i18ntext.Register("notify.writerTask.assigned", map[string]string{
		"zh-CN": "「{user}」把《{book}》的「{chapter}」分配给了你",
		"en":    `{user} assigned "{chapter}" in "{book}" to you`,
	})
	i18ntext.Register("notify.writerTask.review", map[string]string{
		"zh-CN": "《{book}》的「{chapter}」已提交审阅（{user}）",
		"en":    `"{chapter}" in "{book}" is ready for review ({user})`,
	})
}

type writerTaskView struct {
	DocumentID uint        `json:"document_id"`
	Assignee   *collabUser `json:"assignee"`
	Stage      string      `json:"stage"`
	DueAt      *time.Time  `json:"due_at"`
	UpdatedAt  time.Time   `json:"updated_at"`
}

func (a *App) taskViews(list []models.WriterChapterTask) []writerTaskView {
	ids := []uint{}
	for _, t := range list {
		if t.AssigneeID != 0 {
			ids = append(ids, t.AssigneeID)
		}
	}
	users := map[uint]collabUser{}
	if len(ids) > 0 {
		var rows []models.User
		a.DB.Where("id IN ?", ids).Find(&rows)
		for i := range rows {
			users[rows[i].ID] = toCollabUser(&rows[i])
		}
	}
	out := make([]writerTaskView, 0, len(list))
	for _, t := range list {
		v := writerTaskView{DocumentID: t.DocumentID, Stage: t.Stage, DueAt: t.DueAt, UpdatedAt: t.UpdatedAt}
		if u, found := users[t.AssigneeID]; found {
			v.Assignee = &u
		}
		out = append(out, v)
	}
	return out
}

// isBookMember 书籍作者或已接受邀请的协作者（可被指定为负责人）。
func (a *App) isBookMember(book *models.Book, userID uint) bool {
	if userID == book.UserID {
		return true
	}
	var n int64
	a.DB.Model(&models.BookCollaborator{}).Where("book_id = ? AND user_id = ? AND status = ? AND role IN ?", book.ID, userID, "accepted", []string{"editor", "suggester"}).Count(&n)
	return n > 0
}

// ListWriterTasks GET /books/:id/chapter-tasks 本书各章节的分工。
func (a *App) ListWriterTasks(c *gin.Context) {
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	if !a.canSuggestBookContent(currentUser(c), book) {
		fail(c, http.StatusForbidden, "无权查看")
		return
	}
	var list []models.WriterChapterTask
	a.DB.Where("book_id = ?", book.ID).Find(&list)
	ok(c, gin.H{"items": a.taskViews(list)})
}

// UpdateWriterTask PUT /documents/:id/task {assignee_id?, stage?, due_at?}
// assignee_id 为 0 表示取消负责人；due_at 为 null 或空串表示取消截止日期（格式 YYYY-MM-DD 或 RFC 3339）。
func (a *App) UpdateWriterTask(c *gin.Context) {
	doc, book, status := a.findDocument(c)
	if doc == nil {
		fail(c, status, "文档不存在")
		return
	}
	u := currentUser(c)
	if !a.canEditBookContent(u, book) {
		fail(c, http.StatusForbidden, "只有能编辑这本书的人可以设置分工")
		return
	}
	var req map[string]json.RawMessage
	if c.ShouldBindJSON(&req) != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	var task models.WriterChapterTask
	if a.DB.Where("document_id = ?", doc.ID).First(&task).Error != nil {
		task = models.WriterChapterTask{BookID: book.ID, DocumentID: doc.ID, Stage: "todo"}
	}
	oldAssignee, oldStage := task.AssigneeID, task.Stage
	if raw, has := req["assignee_id"]; has {
		var id uint
		if string(raw) != "null" && json.Unmarshal(raw, &id) != nil {
			fail(c, http.StatusBadRequest, "负责人无效")
			return
		}
		if id != 0 && !a.isBookMember(book, id) {
			fail(c, http.StatusBadRequest, "负责人须是书籍作者或协作者（编辑者、建议者）")
			return
		}
		task.AssigneeID = id
	}
	if raw, has := req["stage"]; has {
		var stage string
		if json.Unmarshal(raw, &stage) != nil || !writerTaskStages[stage] {
			fail(c, http.StatusBadRequest, "阶段无效")
			return
		}
		task.Stage = stage
	}
	if raw, has := req["due_at"]; has {
		var due string
		if string(raw) != "null" && json.Unmarshal(raw, &due) != nil {
			fail(c, http.StatusBadRequest, "截止日期无效")
			return
		}
		due = strings.TrimSpace(due)
		if due == "" {
			task.DueAt = nil
		} else {
			parsed, err := time.ParseInLocation("2006-01-02", due, time.Local)
			if err != nil {
				parsed, err = time.Parse(time.RFC3339, due)
			}
			if err != nil {
				fail(c, http.StatusBadRequest, "截止日期格式应为 YYYY-MM-DD")
				return
			}
			task.DueAt = &parsed
		}
	}
	task.UpdatedBy = u.ID
	if err := a.DB.Save(&task).Error; err != nil {
		fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	params := map[string]string{"user": u.PublicName(), "book": book.Title, "chapter": doc.Title}
	payload := map[string]any{"link": "/book/writer/" + book.Slug + "/" + doc.Slug, "book_id": book.ID, "document_id": doc.ID}
	if task.AssigneeID != 0 && task.AssigneeID != oldAssignee && task.AssigneeID != u.ID {
		a.NotifyI18n(task.AssigneeID, "collaboration", "notify.writerTask.assigned", params, payload)
	}
	if task.Stage == "review" && oldStage != "review" {
		for _, uid := range a.bookEditors(book) {
			if uid != u.ID {
				a.NotifyI18n(uid, "collaboration", "notify.writerTask.review", params, payload)
			}
		}
	}
	writerHub.Publish(book.ID, "tasks", gin.H{"doc_id": doc.ID, "origin": c.GetHeader(collabConnHeader)})
	detail := map[string]any{"title": doc.Title}
	if task.AssigneeID != oldAssignee {
		detail["assignee"] = ""
		var assignee models.User
		if task.AssigneeID != 0 && a.DB.First(&assignee, task.AssigneeID).Error == nil {
			detail["assignee"] = assignee.PublicName()
		}
	}
	if task.Stage != oldStage {
		detail["stage"] = task.Stage
	}
	if _, has := req["due_at"]; has {
		detail["due_at"] = ""
		if task.DueAt != nil {
			detail["due_at"] = task.DueAt.Format("2006-01-02")
		}
	}
	if len(detail) > 1 {
		a.recordActivity(book.ID, doc.ID, u.ID, "task.updated", detail)
	}
	ok(c, a.taskViews([]models.WriterChapterTask{task})[0])
}
