package teams

import (
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// 团队作为成员组（plugincore.MemberGroupProvider）：其他插件可整体为团队发放按席位计的权益（如团队会员）。
// 席位优先次序：所有者、管理员、成员，同角色按加入时间；所有者与管理员可以为团队购买。

const groupKind = "team"

func init() {
	plugincore.RegisterMemberGroupProvider(plugincore.MemberGroupProvider{
		Kind:    groupKind,
		Enabled: func(core plugincore.Core) bool { return core.PluginEnabled(pluginKey) },
		Get: func(core plugincore.Core, viewer *models.User, id uint) (plugincore.MemberGroup, bool) {
			var t Team
			if core.Gorm().First(&t, id).Error != nil {
				return plugincore.MemberGroup{}, false
			}
			if viewer == nil { // 系统调用（如履约通知）：只取展示信息
				return groupOf(core, t, false), true
			}
			var m Member
			member := viewer != nil && core.Gorm().Where("team_id = ? AND user_id = ? AND status = ?", id, viewer.ID, "accepted").First(&m).Error == nil
			if !member && !(viewer != nil && core.IsAdmin(viewer)) {
				return plugincore.MemberGroup{}, false
			}
			return groupOf(core, t, member && isManager(&m)), true
		},
		Managed: func(core plugincore.Core, u *models.User) []plugincore.MemberGroup {
			var ids []uint
			core.Gorm().Model(&Member{}).Where("user_id = ? AND status = ? AND role IN ?", u.ID, "accepted", []string{RoleOwner, RoleAdmin}).Pluck("team_id", &ids)
			out := []plugincore.MemberGroup{}
			if len(ids) == 0 {
				return out
			}
			var teams []Team
			core.Gorm().Where("id IN ?", ids).Order("id ASC").Find(&teams)
			for _, t := range teams {
				out = append(out, groupOf(core, t, true))
			}
			return out
		},
		Members: func(core plugincore.Core, id uint) []uint {
			var ids []uint
			core.Gorm().Model(&Member{}).Where("team_id = ? AND status = ?", id, "accepted").
				Order("CASE role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END, created_at ASC, id ASC").Pluck("user_id", &ids)
			return ids
		},
		GroupsOf: func(core plugincore.Core, userID uint) []uint {
			var ids []uint
			core.Gorm().Model(&Member{}).Where("user_id = ? AND status = ?", userID, "accepted").Pluck("team_id", &ids)
			return ids
		},
	})
}

func groupOf(core plugincore.Core, t Team, canPurchase bool) plugincore.MemberGroup {
	var n int64
	core.Gorm().Model(&Member{}).Where("team_id = ? AND status = ?", t.ID, "accepted").Count(&n)
	return plugincore.MemberGroup{Kind: groupKind, ID: t.ID, Name: t.Name, Link: "/teams/" + t.Slug, MemberCount: int(n), CanPurchase: canPurchase}
}
