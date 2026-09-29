package booktranslations

import (
	"strconv"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// 「我的任务」：整本 AI 翻译任务（新建译本 / 同步更新），链接到任务进度页。

const userTaskKind = "translate"

var jobStatusesByTab = map[string][]string{
	plugincore.UserTaskTabActive: {jobRunning, jobPaused},
	plugincore.UserTaskTabDone:   {jobDone},
	plugincore.UserTaskTabFailed: {jobFailed},
}

func init() {
	plugincore.RegisterUserTaskSource(plugincore.UserTaskSource{Kind: userTaskKind, Plugin: plugins.KeyBookTranslations,
		List: func(core plugincore.Core, userID uint, tab string, limit int) []plugincore.UserTask {
			db := core.Gorm()
			if !db.Migrator().HasTable(&TranslateJob{}) {
				return nil
			}
			var jobs []TranslateJob
			db.Where("user_id = ? AND status IN ?", userID, jobStatusesByTab[tab]).Order("created_at DESC, id DESC").Limit(limit).Find(&jobs)
			out := make([]plugincore.UserTask, 0, len(jobs))
			for i := range jobs {
				out = append(out, toUserTask(core, &jobs[i]))
			}
			return out
		}})
}

func toUserTask(core plugincore.Core, job *TranslateJob) plugincore.UserTask {
	t := plugincore.UserTask{Kind: userTaskKind, ID: strconv.FormatUint(uint64(job.ID), 10), Status: job.Status,
		Done: job.Done, Failed: job.Failed, Total: job.Total, Error: job.Error,
		CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt, FinishedAt: job.FinishedAt}
	switch job.Status {
	case jobDone:
		t.Status = plugincore.UserTaskDone
	case jobFailed:
		t.Status = plugincore.UserTaskFailed
	case jobPaused:
		t.Status = plugincore.UserTaskPaused
	default:
		t.Status = plugincore.UserTaskRunning
	}
	var src models.Book
	if core.Gorm().Select("id, slug, title").First(&src, job.SourceBookID).Error == nil {
		t.Title = src.Title
		t.Link = "/book/settings/" + src.Slug + "/ai-translate?job=" + t.ID
	}
	if job.TargetLabel != "" {
		t.Title += " → " + job.TargetLabel
	}
	return t
}

// publishUserTask 任务状态或进度变化时推送给发起人。
func publishUserTask(core plugincore.Core, job *TranslateJob) {
	plugincore.PublishUserTask(core, job.UserID, toUserTask(core, job))
}
