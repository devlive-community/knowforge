package chapterguide

import (
	"strconv"
	"time"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// 「我的任务」：书籍的导读与全书概览生成（逐章排队），按书汇总为一条进行中的任务，显示剩余章节数，
// 推送给书籍作者；全部生成完即结束（导读本身在导读管理页查看，不另列已完成记录）。

const userTaskKind = "chapterGuide"

var activeStates = []string{stateQueued, stateGenerating}

func init() {
	plugincore.RegisterUserTaskSource(plugincore.UserTaskSource{Kind: userTaskKind, Plugin: pluginKey,
		List: func(core plugincore.Core, userID uint, tab string, limit int) []plugincore.UserTask {
			db := core.Gorm()
			if tab != plugincore.UserTaskTabActive || !db.Migrator().HasTable(&Guide{}) {
				return nil
			}
			var ids []uint
			db.Model(&models.Book{}).Where("user_id = ?", userID).Where(
				db.Where("id IN (?)", db.Model(&Guide{}).Select("book_id").Where("status IN ?", activeStates)).
					Or("id IN (?)", db.Model(&Overview{}).Select("book_id").Where("status IN ?", activeStates)),
			).Order("id DESC").Limit(limit).Pluck("id", &ids)
			out := make([]plugincore.UserTask, 0, len(ids))
			for _, id := range ids {
				if t, ok := guideUserTask(core, id); ok {
					out = append(out, t)
				}
			}
			return out
		}})
}

// guideUserTask 书籍当前的导读生成汇总；book 不存在时 ok 为 false。
func guideUserTask(core plugincore.Core, bookID uint) (plugincore.UserTask, bool) {
	db := core.Gorm()
	var book models.Book
	if db.Select("id, user_id, slug, title").First(&book, bookID).Error != nil {
		return plugincore.UserTask{}, false
	}
	var guides []Guide
	db.Select("id, status, updated_at").Where("book_id = ? AND status IN ?", bookID, activeStates).Find(&guides)
	var overview Overview
	overviewActive := db.Select("book_id, status, updated_at").Where("book_id = ? AND status IN ?", bookID, activeStates).First(&overview).Error == nil
	t := plugincore.UserTask{Kind: userTaskKind, ID: strconv.FormatUint(uint64(bookID), 10), Title: book.Title,
		Status: plugincore.UserTaskQueued, Remaining: len(guides), Link: "/book/settings/" + book.Slug + "/chapter-guides"}
	var started, updated time.Time
	track := func(state string, at time.Time) {
		if state == stateGenerating {
			t.Status = plugincore.UserTaskRunning
		}
		if started.IsZero() || at.Before(started) {
			started = at
		}
		if at.After(updated) {
			updated = at
		}
	}
	for _, g := range guides {
		track(g.Status, g.UpdatedAt)
	}
	if overviewActive {
		t.Remaining++
		track(overview.Status, overview.UpdatedAt)
	}
	if t.Remaining == 0 {
		now := time.Now()
		t.Status, started, updated, t.FinishedAt = plugincore.UserTaskDone, now, now, &now
	}
	t.CreatedAt, t.UpdatedAt = started, updated
	return t, true
}

// publishUserTask 导读或概览状态变化时，把该书的汇总推送给书籍作者。
func (b *behavior) publishUserTask(bookID uint) {
	var owner models.Book
	if b.core.Gorm().Select("id, user_id").First(&owner, bookID).Error != nil {
		return
	}
	if t, ok := guideUserTask(b.core, bookID); ok {
		plugincore.PublishUserTask(b.core, owner.UserID, t)
	}
}
