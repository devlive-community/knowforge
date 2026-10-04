// Package teams 团队空间插件：用户可以创建团队、邀请成员并分配角色（所有者 / 管理员 / 成员），把自己的书加入团队共享。
// 书籍加入团队后，团队成员按角色自动获得该书的协作权限：所有者与管理员可编辑，成员按加入时设置的权限（编辑 / 建议 / 只读）。
// 这些权限以协作者记录（team_id 标记来源）的形式维护，搜索、书单、写作台、提及与通知等沿用协作者的既有规则；
// 团队权限不能在书籍的协作者中直接修改，直接邀请的协作者优先。每人可创建的团队数与每个团队的成员数为权益。
package teams

import (
	"strconv"
	"time"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/i18ntext"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

const pluginKey = plugins.KeyTeams

const (
	// PermUse 创建与加入团队、把自己的书加入团队。
	PermUse authz.Permission = "teams:use"
	// PermManage 查看与删除站内所有团队（管理员）。
	PermManage authz.Permission = "teams:manage"
)

const (
	cfgEnabled        = "teams_enabled"
	cfgOwnedBase      = "entitlement_teams_owned"
	cfgMembersBase    = "entitlement_teams_members"
	entOwned          = "teams.owned"
	entMembers        = "teams.members"
	defaultOwned      = 3
	defaultMembers    = 20
	maxEntitlementVal = 100000

	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"

	maxName = 60
	maxDesc = 500
)

// bookRoles 团队成员（非管理员）在团队书籍上的权限，取值与书籍协作者角色一致。
var bookRoles = map[string]bool{"editor": true, "suggester": true, "viewer": true}

// Team 一个团队。
type Team struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"size:100;not null" json:"name"`
	Slug        string    `gorm:"size:80;uniqueIndex;not null" json:"slug"`
	Description string    `gorm:"size:1024" json:"description"`
	OwnerID     uint      `gorm:"index;not null" json:"owner_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Member 团队成员（含待接受的邀请）。
type Member struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	TeamID      uint       `gorm:"uniqueIndex:uk_team_member;not null" json:"team_id"`
	UserID      uint       `gorm:"uniqueIndex:uk_team_member;index;not null" json:"user_id"`
	Role        string     `gorm:"size:16;not null" json:"role"`         // owner | admin | member
	Status      string     `gorm:"size:16;index;not null" json:"status"` // pending | accepted
	InvitedBy   uint       `json:"invited_by"`
	RespondedAt *time.Time `json:"responded_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// Book 团队中的书籍（一本书最多属于一个团队）。MemberRole 为普通成员在该书上的权限。
type Book struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	TeamID     uint      `gorm:"index;not null" json:"team_id"`
	BookID     uint      `gorm:"uniqueIndex;not null" json:"book_id"`
	MemberRole string    `gorm:"size:16;not null" json:"member_role"`
	AddedBy    uint      `json:"added_by"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (Team) TableName() string   { return "teams" }
func (Member) TableName() string { return "team_members" }
func (Book) TableName() string   { return "team_books" }

func init() {
	plugins.Register(plugins.Meta{
		Order:       136,
		Key:         pluginKey,
		Name:        "团队空间",
		Description: "用户可以创建团队、邀请成员并分配角色（所有者、管理员、成员），把自己的书加入团队共享：成员按角色自动获得书籍的编辑、建议或只读权限，成员变动时自动同步。每人可创建的团队数与每个团队的成员数为权益，可由会员/等级提升。默认关闭。",
		Kind:        plugins.KindFeature,
		Builtin:     true,
		EnabledKey:  cfgEnabled,
		Models:      []any{&Team{}, &Member{}, &Book{}},
		Tables:      []string{"teams", "team_members", "team_books"},
		UserPerms:   []authz.Permission{PermUse},
		AdminPerms:  []authz.Permission{PermUse, PermManage},
	})
	plugincore.RegisterBehavior(&behavior{})
	plugincore.RegisterUserDataModels(&Member{})
	plugincore.RegisterBookDataModels(&Book{})
	// 直接协作者被移除或拒绝邀请后，仍属书籍所在团队的成员补回团队权限
	plugincore.OnBookCollaboratorsChanged(func(core plugincore.Core, bookID uint) {
		if !core.PluginEnabled(pluginKey) {
			return
		}
		var link Book
		if core.Gorm().Where("book_id = ?", bookID).First(&link).Error == nil {
			(&behavior{core: core}).syncBook(link.TeamID, bookID)
		}
	})
	// 关闭插件后收回团队授予的权限，重新启用时恢复
	plugincore.OnPluginEnabled(pluginKey, func(core plugincore.Core) error {
		var ids []uint
		core.Gorm().Model(&Team{}).Pluck("id", &ids)
		b := &behavior{core: core}
		for _, id := range ids {
			b.syncTeam(id)
		}
		return nil
	})
	plugincore.OnPluginDisabled(pluginKey, func(core plugincore.Core) error {
		return core.Gorm().Where("team_id <> 0").Delete(&models.BookCollaborator{}).Error
	})
	limit := func(key, cfg string, def int64, label string) plugincore.EntitlementDef {
		return plugincore.EntitlementDef{
			Key: key, Kind: plugincore.EntitlementLimit, Unit: "people", Min: 0, Max: maxEntitlementVal, AllowUnlimited: true, Order: 36,
			Available: func(core plugincore.Core) bool { return core.PluginEnabled(pluginKey) },
			Base: func(core plugincore.Core) int64 {
				if v, err := strconv.ParseInt(core.GetSetting(cfg), 10, 64); err == nil && (v == plugincore.Unlimited || (v >= 0 && v <= maxEntitlementVal)) {
					return v
				}
				return def
			},
			SetBase: func(core plugincore.Core, v int64) error {
				return core.SetSetting(cfg, strconv.FormatInt(v, 10), label)
			},
		}
	}
	owned := limit(entOwned, cfgOwnedBase, defaultOwned, "权益：可创建的团队数（基础）")
	owned.Unit = "teams"
	plugincore.RegisterEntitlement(owned)
	members := limit(entMembers, cfgMembersBase, defaultMembers, "权益：每个团队的成员数（基础，按团队所有者计）")
	members.Order = 37
	plugincore.RegisterEntitlement(members)

	for key, texts := range map[string][2]string{
		"notify.team.invited":  {"{user} 邀请你加入团队「{team}」", "{user} invited you to join the team \"{team}\""},
		"notify.team.accepted": {"{user} 加入了团队「{team}」", "{user} joined the team \"{team}\""},
		"notify.team.declined": {"{user} 拒绝了加入团队「{team}」的邀请", "{user} declined to join the team \"{team}\""},
		"notify.team.removed":  {"你已被移出团队「{team}」", "You were removed from the team \"{team}\""},
		"notify.team.owner":    {"{user} 把团队「{team}」转交给了你", "{user} made you the owner of the team \"{team}\""},
	} {
		i18ntext.Register(key, map[string]string{"zh-CN": texts[0], "en": texts[1]})
	}
}

type behavior struct{ core plugincore.Core }

func (b *behavior) Key() string { return pluginKey }
