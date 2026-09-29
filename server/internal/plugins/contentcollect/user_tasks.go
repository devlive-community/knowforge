package contentcollect

import (
	"strconv"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// 「我的任务」：整站采集任务（后台逐页采集），链接到本书的采集历史详情。单页/单章采集即时完成，不列入。

const userTaskKind = "siteCrawl"

var crawlStatusesByTab = map[string][]string{
	plugincore.UserTaskTabActive: {"pending", "running"},
	plugincore.UserTaskTabDone:   {"succeeded", "partial"},
	plugincore.UserTaskTabFailed: {"failed"},
}

func init() {
	plugincore.RegisterUserTaskSource(plugincore.UserTaskSource{Kind: userTaskKind, Plugin: plugins.KeyContentCollect,
		List: func(core plugincore.Core, userID uint, tab string, limit int) []plugincore.UserTask {
			db := core.Gorm()
			if !db.Migrator().HasTable(&CrawlJob{}) {
				return nil
			}
			var jobs []CrawlJob
			db.Where("user_id = ? AND kind = ? AND status IN ?", userID, "site", crawlStatusesByTab[tab]).
				Order("created_at DESC, id DESC").Limit(limit).Find(&jobs)
			out := make([]plugincore.UserTask, 0, len(jobs))
			for i := range jobs {
				out = append(out, crawlUserTask(core, &jobs[i]))
			}
			return out
		}})
}

func crawlUserTask(core plugincore.Core, job *CrawlJob) plugincore.UserTask {
	t := plugincore.UserTask{Kind: userTaskKind, ID: strconv.FormatUint(uint64(job.ID), 10), Title: job.RootURL,
		Done: job.Success, Failed: job.Failed, Total: job.Total, Error: job.LastError,
		CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt, FinishedAt: job.FinishedAt}
	switch job.Status {
	case "running":
		t.Status = plugincore.UserTaskRunning
	case "succeeded", "partial":
		t.Status = plugincore.UserTaskDone
	case "failed":
		t.Status = plugincore.UserTaskFailed
	default:
		t.Status = plugincore.UserTaskQueued
	}
	var book models.Book
	if core.Gorm().Select("id, slug, title").First(&book, job.BookID).Error == nil {
		t.Title = book.Title
		t.Link = "/book/settings/" + book.Slug + "/crawl-history?kind=site&job=" + t.ID
	}
	return t
}

// publishCrawlTask 重新读取任务并推送给发起人。
func (cc *behavior) publishCrawlTask(id uint) {
	var job CrawlJob
	if cc.core.Gorm().First(&job, id).Error == nil && job.Kind == "site" {
		plugincore.PublishUserTask(cc.core, job.UserID, crawlUserTask(cc.core, &job))
	}
}
