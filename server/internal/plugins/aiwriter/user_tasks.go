package aiwriter

import (
	"strconv"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// 「我的任务」：写作助手的后台生成，链接到对应章节的写作页（在写作助手记录中查看结果）。

const userTaskKind = "aiWriter"

var taskStatusesByTab = map[string][]string{
	plugincore.UserTaskTabActive: {statusRunning},
	plugincore.UserTaskTabDone:   {statusDone},
	plugincore.UserTaskTabFailed: {statusFailed, statusCanceled},
}

func init() {
	plugincore.RegisterUserTaskSource(plugincore.UserTaskSource{Kind: userTaskKind, Plugin: plugins.KeyAIWriter,
		List: func(core plugincore.Core, userID uint, tab string, limit int) []plugincore.UserTask {
			db := core.Gorm()
			if !db.Migrator().HasTable(&Task{}) {
				return nil
			}
			var tasks []Task
			db.Select("id, user_id, book_id, doc_id, action, status, error, created_at, updated_at").
				Where("user_id = ? AND status IN ?", userID, taskStatusesByTab[tab]).Order("created_at DESC, id DESC").Limit(limit).Find(&tasks)
			out := make([]plugincore.UserTask, 0, len(tasks))
			for i := range tasks {
				out = append(out, writerUserTask(core, &tasks[i]))
			}
			return out
		}})
}

func writerUserTask(core plugincore.Core, task *Task) plugincore.UserTask {
	t := plugincore.UserTask{Kind: userTaskKind, ID: strconv.FormatUint(uint64(task.ID), 10), Error: task.Error,
		CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt}
	switch task.Status {
	case statusDone:
		t.Status = plugincore.UserTaskDone
	case statusFailed:
		t.Status = plugincore.UserTaskFailed
	case statusCanceled:
		t.Status = plugincore.UserTaskCanceled
	default:
		t.Status = plugincore.UserTaskRunning
	}
	if t.Status != plugincore.UserTaskRunning {
		finished := task.UpdatedAt
		t.FinishedAt = &finished
	}
	db := core.Gorm()
	var book models.Book
	if db.Select("id, slug, title").First(&book, task.BookID).Error == nil {
		t.Title = book.Title
		t.Link = "/book/writer/" + book.Slug
		var doc models.Document
		if task.DocID != 0 && db.Select("id, slug, title").Where("book_id = ?", book.ID).First(&doc, task.DocID).Error == nil {
			t.Title += " · " + doc.Title
			t.Link += "/" + doc.Slug
		}
	}
	return t
}

// publishUserTask 生成开始或结束时推送给发起人。
func (b *behavior) publishUserTask(id uint) {
	var task Task
	if b.core.Gorm().First(&task, id).Error == nil {
		plugincore.PublishUserTask(b.core, task.UserID, writerUserTask(b.core, &task))
	}
}
