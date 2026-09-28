package moderation

import (
	"strconv"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// AI 调用记录的关联：AI 审核的调用（仅管理员可见）跳转到内容审核。
func init() {
	plugincore.RegisterAIUsageRef("moderation.", func(core plugincore.Core, viewer *models.User, in plugincore.AIUsageRefInput) *plugincore.AIUsageRef {
		if !core.IsAdmin(viewer) || in.RefID == 0 {
			return nil
		}
		return &plugincore.AIUsageRef{Kind: "moderation_case", Title: "#" + strconv.FormatUint(uint64(in.RefID), 10), Link: "/admin/moderation"}
	})
}
