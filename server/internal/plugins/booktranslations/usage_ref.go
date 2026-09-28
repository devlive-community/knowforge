package booktranslations

import (
	"strconv"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// AI 调用记录的关联：书籍翻译的调用链对应一个翻译任务，可编辑原书的人（含管理员）跳转到任务进度，
// 其他情况回退为译本书籍。
func init() {
	plugincore.RegisterAIUsageRef(featureBookTranslate, func(core plugincore.Core, viewer *models.User, in plugincore.AIUsageRefInput) *plugincore.AIUsageRef {
		var job TranslateJob
		if in.TraceID == "" || !core.Gorm().Migrator().HasTable(&TranslateJob{}) || core.Gorm().Where("trace_id = ?", in.TraceID).First(&job).Error != nil {
			return nil
		}
		var source models.Book
		if core.Gorm().First(&source, job.SourceBookID).Error != nil {
			return nil
		}
		if !core.IsAdmin(viewer) && !core.CanEditBookContent(viewer, &source) {
			return nil
		}
		title := source.Title
		if job.TargetLabel != "" {
			title += " → " + job.TargetLabel
		}
		return &plugincore.AIUsageRef{Kind: "translation", Title: title,
			Link: "/book/settings/" + source.Slug + "/ai-translate?job=" + strconv.FormatUint(uint64(job.ID), 10)}
	})
}
