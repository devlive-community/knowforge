package teams

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

func (b *behavior) RegisterRoutes(api *gin.RouterGroup, core plugincore.Core) {
	b.core = core
	feat := core.RequireFeaturePlugin(pluginKey)
	use := core.RequirePermissionMiddleware(PermUse)
	with := func(h gin.HandlerFunc) []gin.HandlerFunc { return []gin.HandlerFunc{core.RequireAuth(), feat, use, h} }
	api.GET("/teams", with(b.ListMine)...)
	api.POST("/teams", with(b.Create)...)
	api.GET("/teams/:id", with(b.Detail)...)
	api.PUT("/teams/:id", with(b.Update)...)
	api.DELETE("/teams/:id", with(b.Delete)...)
	api.POST("/teams/:id/transfer", with(b.Transfer)...)
	api.POST("/teams/:id/members", with(b.Invite)...)
	api.PUT("/teams/:id/members/:userId", with(b.UpdateMember)...)
	api.DELETE("/teams/:id/members/:userId", with(b.RemoveMember)...)
	api.POST("/teams/:id/books", with(b.AddBook)...)
	api.PUT("/teams/:id/books/:bookId", with(b.UpdateBook)...)
	api.DELETE("/teams/:id/books/:bookId", with(b.RemoveBook)...)
	api.POST("/team-invitations/:id/accept", with(b.AcceptInvitation)...)
	api.POST("/team-invitations/:id/decline", with(b.DeclineInvitation)...)
	api.GET("/team-books/:bookId", with(b.BookTeam)...)
	admin := func(h gin.HandlerFunc) []gin.HandlerFunc {
		return []gin.HandlerFunc{core.RequireAuth(), core.RequireAdmin(), feat, core.RequirePermissionMiddleware(PermManage), h}
	}
	api.GET("/admin/teams", admin(b.AdminList)...)
	api.DELETE("/admin/teams/:id", admin(b.AdminDelete)...)
}

// —— 视图 ——

type userBrief struct {
	ID          uint   `json:"id"`
	Username    string `json:"username"`
	Nickname    string `json:"nickname"`
	NameDisplay string `json:"name_display"`
	Avatar      string `json:"avatar"`
}

type teamView struct {
	Team
	MyRole      string `json:"my_role,omitempty"`
	MemberCount int64  `json:"member_count"`
	BookCount   int64  `json:"book_count"`
}

type memberView struct {
	ID        uint      `json:"id"`
	User      userBrief `json:"user"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

func (b *behavior) users(ids []uint) map[uint]userBrief {
	out := map[uint]userBrief{}
	if len(ids) == 0 {
		return out
	}
	var rows []models.User
	b.core.Gorm().Select("id", "username", "nickname", "name_display", "avatar").Where("id IN ?", ids).Find(&rows)
	for _, u := range rows {
		out[u.ID] = userBrief{ID: u.ID, Username: u.Username, Nickname: u.Nickname, NameDisplay: u.NameDisplay, Avatar: u.Avatar}
	}
	return out
}

func (b *behavior) view(t Team, myRole string) teamView {
	v := teamView{Team: t, MyRole: myRole}
	b.core.Gorm().Model(&Member{}).Where("team_id = ? AND status = ?", t.ID, "accepted").Count(&v.MemberCount)
	b.core.Gorm().Model(&Book{}).Joins("JOIN books ON books.id = team_books.book_id AND books.deleted_at IS NULL").Where("team_books.team_id = ?", t.ID).Count(&v.BookCount)
	return v
}

// —— 查找与权限 ——

// loadTeam 按 :id（数字 ID 或 slug）查找团队，返回当前用户在团队中的成员记录（非成员为 nil）。
// 非成员（站点管理员除外）一律视为不存在，不暴露团队信息。
func (b *behavior) loadTeam(c *gin.Context) (*Team, *Member, bool) {
	db := b.core.Gorm()
	var t Team
	key := c.Param("id")
	q := db.Where("slug = ?", key)
	if id, err := strconv.ParseUint(key, 10, 64); err == nil {
		q = db.Where("id = ?", id)
	}
	u := b.core.CurrentUser(c)
	if q.First(&t).Error != nil || !b.ensureOwner(&t) {
		b.core.Fail(c, http.StatusNotFound, "团队不存在")
		return nil, nil, false
	}
	var m Member
	if db.Where("team_id = ? AND user_id = ? AND status = ?", t.ID, u.ID, "accepted").First(&m).Error != nil {
		if b.core.IsAdmin(u) {
			return &t, nil, true
		}
		b.core.Fail(c, http.StatusNotFound, "团队不存在")
		return nil, nil, false
	}
	return &t, &m, true
}

// ensureOwner 所有者注销账号后（其成员记录随账号删除）：由最早加入的管理员接任，没有管理员则由最早加入的成员接任；
// 已没有成员时解散团队并返回 false。
func (b *behavior) ensureOwner(t *Team) bool {
	db := b.core.Gorm()
	var n int64
	db.Model(&Member{}).Where("team_id = ? AND role = ? AND status = ?", t.ID, RoleOwner, "accepted").Count(&n)
	if n > 0 {
		return true
	}
	var next Member
	if db.Where("team_id = ? AND status = ?", t.ID, "accepted").Order("CASE WHEN role = 'admin' THEN 0 ELSE 1 END, created_at ASC").First(&next).Error != nil {
		_ = b.deleteTeam(t)
		return false
	}
	db.Model(&next).Update("role", RoleOwner)
	db.Model(t).Update("owner_id", next.UserID)
	t.OwnerID = next.UserID
	b.syncTeam(t.ID)
	return true
}

func isManager(m *Member) bool { return m != nil && (m.Role == RoleOwner || m.Role == RoleAdmin) }

func (b *behavior) entitlement(userID uint, key string) int64 {
	var u models.User
	if b.core.Gorm().First(&u, userID).Error != nil {
		return 0
	}
	return plugincore.EntitlementValue(b.core, &u, key)
}

func parseID(c *gin.Context, name string) (uint, bool) {
	id, err := strconv.ParseUint(c.Param(name), 10, 64)
	return uint(id), err == nil && id > 0
}

func cleanText(s string, max int) (string, bool) {
	s = strings.TrimSpace(s)
	return s, utf8.RuneCountInString(s) <= max
}

// uniqueSlug 由团队名生成唯一的 slug（名称无法生成时用随机串）。
func (b *behavior) uniqueSlug(name string) string {
	base := b.core.Slugify(name)
	if base == "" || len(base) > 60 {
		base = b.core.RandomSlug("team")
	}
	slug := base
	for i := 2; ; i++ {
		var n int64
		b.core.Gorm().Model(&Team{}).Where("slug = ?", slug).Count(&n)
		if n == 0 {
			return slug
		}
		if i > 50 {
			return b.core.RandomSlug("team")
		}
		slug = fmt.Sprintf("%s-%d", base, i)
	}
}

// —— 我的团队 ——

type invitationView struct {
	ID        uint      `json:"id"`
	Team      Team      `json:"team"`
	Role      string    `json:"role"`
	Inviter   userBrief `json:"inviter"`
	CreatedAt time.Time `json:"created_at"`
}

// ListMine GET /teams 我加入的团队与待接受的邀请，以及创建团队的额度。
func (b *behavior) ListMine(c *gin.Context) {
	u := b.core.CurrentUser(c)
	db := b.core.Gorm()
	var mine []Member
	db.Where("user_id = ?", u.ID).Order("created_at ASC").Find(&mine)
	teams := []teamView{}
	invitations := []invitationView{}
	var inviterIDs []uint
	for _, m := range mine {
		if m.Status == "pending" {
			inviterIDs = append(inviterIDs, m.InvitedBy)
		}
	}
	inviters := b.users(inviterIDs)
	for _, m := range mine {
		var t Team
		if db.First(&t, m.TeamID).Error != nil || !b.ensureOwner(&t) {
			continue
		}
		if m.Status == "accepted" {
			teams = append(teams, b.view(t, m.Role))
		} else if m.Status == "pending" {
			invitations = append(invitations, invitationView{ID: m.ID, Team: t, Role: m.Role, Inviter: inviters[m.InvitedBy], CreatedAt: m.UpdatedAt})
		}
	}
	var owned int64
	db.Model(&Team{}).Where("owner_id = ?", u.ID).Count(&owned)
	b.core.OK(c, gin.H{"teams": teams, "invitations": invitations, "owned": owned, "owned_limit": plugincore.EntitlementValue(b.core, u, entOwned)})
}

type teamPayload struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// Create POST /teams {name, description} 创建团队，创建者为所有者（受「可创建的团队数」权益限制）。
func (b *behavior) Create(c *gin.Context) {
	u := b.core.CurrentUser(c)
	var req teamPayload
	if c.ShouldBindJSON(&req) != nil || req.Name == nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	name, okName := cleanText(*req.Name, maxName)
	desc := ""
	okDesc := true
	if req.Description != nil {
		desc, okDesc = cleanText(*req.Description, maxDesc)
	}
	if name == "" || !okName || !okDesc {
		b.core.Fail(c, http.StatusBadRequest, fmt.Sprintf("团队名称为 1 到 %d 个字，简介不超过 %d 字", maxName, maxDesc))
		return
	}
	db := b.core.Gorm()
	var owned int64
	db.Model(&Team{}).Where("owner_id = ?", u.ID).Count(&owned)
	if limit := plugincore.EntitlementValue(b.core, u, entOwned); !plugincore.WithinLimit(limit, owned) {
		b.core.Fail(c, http.StatusForbidden, fmt.Sprintf("你创建的团队已达上限（%d 个），升级等级或开通会员可创建更多团队", limit))
		return
	}
	t := Team{Name: name, Slug: b.uniqueSlug(name), Description: desc, OwnerID: u.ID}
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&t).Error; err != nil {
			return err
		}
		now := time.Now()
		return tx.Create(&Member{TeamID: t.ID, UserID: u.ID, Role: RoleOwner, Status: "accepted", InvitedBy: u.ID, RespondedAt: &now}).Error
	})
	if err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "创建失败")
		return
	}
	b.core.OK(c, b.view(t, RoleOwner))
}

type bookView struct {
	models.Book
	MemberRole string    `json:"member_role"`
	AddedBy    uint      `json:"added_by"`
	AddedAt    time.Time `json:"added_at"`
}

// Detail GET /teams/:id 团队详情（成员可见）：成员（管理者另见待接受的邀请）与团队书籍。
func (b *behavior) Detail(c *gin.Context) {
	t, me, found := b.loadTeam(c)
	if !found {
		return
	}
	db := b.core.Gorm()
	myRole := ""
	if me != nil {
		myRole = me.Role
	}
	manage := isManager(me) || (me == nil && b.core.IsAdmin(b.core.CurrentUser(c)))
	var members []Member
	q := db.Where("team_id = ?", t.ID)
	if !manage {
		q = q.Where("status = ?", "accepted")
	}
	q.Find(&members)
	ids := make([]uint, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.UserID)
	}
	users := b.users(ids)
	rank := map[string]int{RoleOwner: 0, RoleAdmin: 1, RoleMember: 2}
	memberViews := make([]memberView, 0, len(members))
	for _, m := range members {
		memberViews = append(memberViews, memberView{ID: m.ID, User: users[m.UserID], Role: m.Role, Status: m.Status, CreatedAt: m.CreatedAt})
	}
	sortMembers(memberViews, rank)

	var links []Book
	db.Where("team_id = ?", t.ID).Order("created_at DESC").Find(&links)
	bookIDs := make([]uint, 0, len(links))
	for _, l := range links {
		bookIDs = append(bookIDs, l.BookID)
	}
	var books []models.Book
	if len(bookIDs) > 0 {
		b.core.PreloadBookUser().Where("id IN ?", bookIDs).Find(&books)
		b.core.AttachChapterCounts(books)
		b.core.DecorateBookList(books)
	}
	byID := map[uint]models.Book{}
	for _, bk := range books {
		byID[bk.ID] = bk
	}
	bookViews := []bookView{}
	for _, l := range links {
		if bk, has := byID[l.BookID]; has {
			bookViews = append(bookViews, bookView{Book: bk, MemberRole: l.MemberRole, AddedBy: l.AddedBy, AddedAt: l.CreatedAt})
		}
	}
	var used int64
	db.Model(&Member{}).Where("team_id = ? AND status IN ?", t.ID, []string{"pending", "accepted"}).Count(&used)
	b.core.OK(c, gin.H{
		"team": b.view(*t, myRole), "members": memberViews, "books": bookViews, "can_manage": manage,
		"seats": gin.H{"used": used, "limit": b.entitlement(t.OwnerID, entMembers)},
	})
}

func sortMembers(ms []memberView, rank map[string]int) {
	for i := 1; i < len(ms); i++ {
		for j := i; j > 0; j-- {
			a, z := ms[j-1], ms[j]
			less := rank[z.Role] < rank[a.Role] || (rank[z.Role] == rank[a.Role] && z.CreatedAt.Before(a.CreatedAt))
			if !less {
				break
			}
			ms[j-1], ms[j] = z, a
		}
	}
}

// Update PUT /teams/:id {name?, description?}（所有者 / 管理员）
func (b *behavior) Update(c *gin.Context) {
	t, me, found := b.loadTeam(c)
	if !found {
		return
	}
	if !isManager(me) {
		b.core.Fail(c, http.StatusForbidden, "只有团队所有者和管理员可以修改团队信息")
		return
	}
	var req teamPayload
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	updates := map[string]any{}
	if req.Name != nil {
		name, valid := cleanText(*req.Name, maxName)
		if name == "" || !valid {
			b.core.Fail(c, http.StatusBadRequest, fmt.Sprintf("团队名称为 1 到 %d 个字", maxName))
			return
		}
		updates["name"] = name
	}
	if req.Description != nil {
		desc, valid := cleanText(*req.Description, maxDesc)
		if !valid {
			b.core.Fail(c, http.StatusBadRequest, fmt.Sprintf("简介不超过 %d 字", maxDesc))
			return
		}
		updates["description"] = desc
	}
	if len(updates) > 0 {
		b.core.Gorm().Model(t).Updates(updates)
	}
	b.core.Gorm().First(t, t.ID)
	b.core.OK(c, b.view(*t, me.Role))
}

// deleteTeam 删除团队：收回团队授予的书籍权限，书籍仍归各自作者。
func (b *behavior) deleteTeam(t *Team) error {
	return b.core.Gorm().Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("team_id = ?", t.ID).Delete(&models.BookCollaborator{}).Error; err != nil {
			return err
		}
		if err := tx.Where("team_id = ?", t.ID).Delete(&Book{}).Error; err != nil {
			return err
		}
		if err := tx.Where("team_id = ?", t.ID).Delete(&Member{}).Error; err != nil {
			return err
		}
		return tx.Delete(t).Error
	})
}

// Delete DELETE /teams/:id（所有者）
func (b *behavior) Delete(c *gin.Context) {
	t, me, found := b.loadTeam(c)
	if !found {
		return
	}
	if me == nil || me.Role != RoleOwner {
		b.core.Fail(c, http.StatusForbidden, "只有团队所有者可以解散团队")
		return
	}
	if err := b.deleteTeam(t); err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "解散失败")
		return
	}
	b.core.OK(c, gin.H{"deleted": true})
}

// Transfer POST /teams/:id/transfer {user_id} 所有者把团队转交给另一位成员，自己改为管理员。
func (b *behavior) Transfer(c *gin.Context) {
	t, me, found := b.loadTeam(c)
	if !found {
		return
	}
	if me == nil || me.Role != RoleOwner {
		b.core.Fail(c, http.StatusForbidden, "只有团队所有者可以转交团队")
		return
	}
	var req struct {
		UserID uint `json:"user_id"`
	}
	if c.ShouldBindJSON(&req) != nil || req.UserID == 0 || req.UserID == me.UserID {
		b.core.Fail(c, http.StatusBadRequest, "请选择要转交的成员")
		return
	}
	db := b.core.Gorm()
	var target Member
	if db.Where("team_id = ? AND user_id = ? AND status = ?", t.ID, req.UserID, "accepted").First(&target).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "只能转交给团队中的成员")
		return
	}
	var owned int64
	db.Model(&Team{}).Where("owner_id = ?", req.UserID).Count(&owned)
	if limit := b.entitlement(req.UserID, entOwned); !plugincore.WithinLimit(limit, owned) {
		b.core.Fail(c, http.StatusForbidden, "对方拥有的团队已达上限，无法接收")
		return
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Member{}).Where("id = ?", target.ID).Update("role", RoleOwner).Error; err != nil {
			return err
		}
		if err := tx.Model(&Member{}).Where("id = ?", me.ID).Update("role", RoleAdmin).Error; err != nil {
			return err
		}
		return tx.Model(t).Update("owner_id", req.UserID).Error
	})
	if err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "转交失败")
		return
	}
	b.syncTeam(t.ID)
	u := b.core.CurrentUser(c)
	b.core.NotifyI18n(req.UserID, "collaboration", "notify.team.owner", map[string]string{"user": u.Username, "team": t.Name}, map[string]any{"link": "/teams/" + t.Slug})
	b.core.OK(c, gin.H{"owner_id": req.UserID})
}

// —— 成员 ——

type memberPayload struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

// Invite POST /teams/:id/members {username, role} 邀请成员（所有者可邀请管理员，管理员只能邀请成员）；对方接受后加入。
// 已是成员的不重复邀请；成员数（含待接受）受团队所有者的「每个团队的成员数」权益限制。
func (b *behavior) Invite(c *gin.Context) {
	t, me, found := b.loadTeam(c)
	if !found {
		return
	}
	if !isManager(me) {
		b.core.Fail(c, http.StatusForbidden, "只有团队所有者和管理员可以邀请成员")
		return
	}
	var req memberPayload
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if req.Role == "" {
		req.Role = RoleMember
	}
	if req.Role != RoleAdmin && req.Role != RoleMember {
		b.core.Fail(c, http.StatusBadRequest, "角色必须为管理员或成员")
		return
	}
	if req.Role == RoleAdmin && me.Role != RoleOwner {
		b.core.Fail(c, http.StatusForbidden, "只有团队所有者可以邀请管理员")
		return
	}
	db := b.core.Gorm()
	var target models.User
	if db.Where("username = ?", strings.TrimSpace(req.Username)).First(&target).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "用户不存在")
		return
	}
	if !target.IsActive {
		b.core.Fail(c, http.StatusForbidden, "该账户已被禁用")
		return
	}
	var existing Member
	if db.Where("team_id = ? AND user_id = ?", t.ID, target.ID).First(&existing).Error == nil {
		if existing.Status == "accepted" {
			b.core.Fail(c, http.StatusConflict, "该用户已是团队成员")
			return
		}
		b.core.Fail(c, http.StatusConflict, "已邀请过该用户，等待对方接受")
		return
	}
	var used int64
	db.Model(&Member{}).Where("team_id = ? AND status IN ?", t.ID, []string{"pending", "accepted"}).Count(&used)
	if limit := b.entitlement(t.OwnerID, entMembers); !plugincore.WithinLimit(limit, used) {
		b.core.Fail(c, http.StatusForbidden, fmt.Sprintf("团队成员已达上限（%d 人），团队所有者升级等级或开通会员可容纳更多成员", limit))
		return
	}
	u := b.core.CurrentUser(c)
	m := Member{TeamID: t.ID, UserID: target.ID, Role: req.Role, Status: "pending", InvitedBy: u.ID}
	if err := db.Create(&m).Error; err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "邀请失败")
		return
	}
	b.core.NotifyI18n(target.ID, "collaboration", "notify.team.invited", map[string]string{"user": u.Username, "team": t.Name}, map[string]any{"link": "/teams"})
	b.core.OK(c, memberView{ID: m.ID, User: b.users([]uint{target.ID})[target.ID], Role: m.Role, Status: m.Status, CreatedAt: m.CreatedAt})
}

// UpdateMember PUT /teams/:id/members/:userId {role} 所有者调整成员角色（管理员 / 成员）。
func (b *behavior) UpdateMember(c *gin.Context) {
	t, me, found := b.loadTeam(c)
	if !found {
		return
	}
	userID, valid := parseID(c, "userId")
	if !valid {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if me == nil || me.Role != RoleOwner {
		b.core.Fail(c, http.StatusForbidden, "只有团队所有者可以调整成员角色")
		return
	}
	var req memberPayload
	if c.ShouldBindJSON(&req) != nil || (req.Role != RoleAdmin && req.Role != RoleMember) {
		b.core.Fail(c, http.StatusBadRequest, "角色必须为管理员或成员")
		return
	}
	db := b.core.Gorm()
	var m Member
	if db.Where("team_id = ? AND user_id = ?", t.ID, userID).First(&m).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "该用户不是团队成员")
		return
	}
	if m.Role == RoleOwner {
		b.core.Fail(c, http.StatusBadRequest, "所有者的角色不能调整，请先转交团队")
		return
	}
	db.Model(&m).Update("role", req.Role)
	b.syncTeam(t.ID)
	b.core.OK(c, gin.H{"user_id": userID, "role": req.Role})
}

// RemoveMember DELETE /teams/:id/members/:userId 移除成员或撤回邀请（所有者可移除任何人；管理员可移除普通成员），
// 成员也可以自行退出（所有者需先转交团队）。移除后收回其团队书籍权限。
func (b *behavior) RemoveMember(c *gin.Context) {
	t, me, found := b.loadTeam(c)
	if !found {
		return
	}
	userID, valid := parseID(c, "userId")
	if !valid {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	db := b.core.Gorm()
	var m Member
	if db.Where("team_id = ? AND user_id = ?", t.ID, userID).First(&m).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "该用户不是团队成员")
		return
	}
	self := me != nil && me.UserID == userID
	switch {
	case m.Role == RoleOwner:
		b.core.Fail(c, http.StatusBadRequest, "所有者不能退出团队，请先转交团队或解散团队")
		return
	case self:
	case me != nil && me.Role == RoleOwner:
	case me != nil && me.Role == RoleAdmin && m.Role == RoleMember:
	default:
		b.core.Fail(c, http.StatusForbidden, "无权移除该成员")
		return
	}
	if err := db.Delete(&m).Error; err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "移除失败")
		return
	}
	b.syncTeam(t.ID)
	if !self && m.Status == "accepted" {
		b.core.NotifyI18n(userID, "collaboration", "notify.team.removed", map[string]string{"team": t.Name}, map[string]any{"link": "/teams"})
	}
	b.core.OK(c, gin.H{"removed": true})
}

func (b *behavior) respondInvitation(c *gin.Context, accept bool) {
	u := b.core.CurrentUser(c)
	id, valid := parseID(c, "id")
	if !valid {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	db := b.core.Gorm()
	var m Member
	if db.Where("id = ? AND user_id = ? AND status = ?", id, u.ID, "pending").First(&m).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "邀请不存在或已处理")
		return
	}
	var t Team
	if db.First(&t, m.TeamID).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "团队不存在")
		return
	}
	key := "notify.team.declined"
	if accept {
		now := time.Now()
		res := db.Model(&Member{}).Where("id = ? AND status = ?", m.ID, "pending").Updates(map[string]any{"status": "accepted", "responded_at": &now})
		if res.Error != nil || res.RowsAffected == 0 {
			b.core.Fail(c, http.StatusConflict, "邀请已被处理")
			return
		}
		b.syncTeam(t.ID)
		key = "notify.team.accepted"
	} else if err := db.Delete(&m).Error; err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "处理邀请失败")
		return
	}
	if m.InvitedBy != 0 && m.InvitedBy != u.ID {
		b.core.NotifyI18n(m.InvitedBy, "collaboration", key, map[string]string{"user": u.Username, "team": t.Name}, map[string]any{"link": "/teams/" + t.Slug + "?tab=members"})
	}
	b.core.OK(c, gin.H{"team": t, "accepted": accept})
}

// AcceptInvitation POST /team-invitations/:id/accept
func (b *behavior) AcceptInvitation(c *gin.Context) { b.respondInvitation(c, true) }

// DeclineInvitation POST /team-invitations/:id/decline
func (b *behavior) DeclineInvitation(c *gin.Context) { b.respondInvitation(c, false) }

// —— 团队书籍 ——

type bookPayload struct {
	BookID     uint   `json:"book_id"`
	MemberRole string `json:"member_role"`
}

// AddBook POST /teams/:id/books {book_id, member_role} 团队成员把自己管理的书加入团队（一本书只能属于一个团队）。
func (b *behavior) AddBook(c *gin.Context) {
	t, me, found := b.loadTeam(c)
	if !found {
		return
	}
	if me == nil {
		b.core.Fail(c, http.StatusForbidden, "只有团队成员可以加入书籍")
		return
	}
	var req bookPayload
	if c.ShouldBindJSON(&req) != nil || req.BookID == 0 {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if req.MemberRole == "" {
		req.MemberRole = "editor"
	}
	if !bookRoles[req.MemberRole] {
		b.core.Fail(c, http.StatusBadRequest, "成员权限必须为编辑、建议或只读")
		return
	}
	db := b.core.Gorm()
	var book models.Book
	u := b.core.CurrentUser(c)
	if db.First(&book, req.BookID).Error != nil || book.UserID != u.ID {
		b.core.Fail(c, http.StatusForbidden, "只能把自己创建的书加入团队")
		return
	}
	var existing Book
	if db.Where("book_id = ?", book.ID).First(&existing).Error == nil {
		b.core.Fail(c, http.StatusConflict, "这本书已属于一个团队，请先移出")
		return
	}
	link := Book{TeamID: t.ID, BookID: book.ID, MemberRole: req.MemberRole, AddedBy: u.ID}
	if err := db.Create(&link).Error; err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "加入失败")
		return
	}
	b.syncBook(t.ID, book.ID)
	b.core.OK(c, link)
}

// linkFor 团队中的一本书，以及当前用户能否调整它（团队所有者 / 管理员，或书籍作者）。
func (b *behavior) linkFor(c *gin.Context) (*Team, *Book, bool) {
	t, me, found := b.loadTeam(c)
	if !found {
		return nil, nil, false
	}
	bookID, valid := parseID(c, "bookId")
	if !valid {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return nil, nil, false
	}
	db := b.core.Gorm()
	var link Book
	if db.Where("team_id = ? AND book_id = ?", t.ID, bookID).First(&link).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "这本书不在团队中")
		return nil, nil, false
	}
	var book models.Book
	u := b.core.CurrentUser(c)
	author := db.Select("id", "user_id").First(&book, bookID).Error == nil && book.UserID == u.ID
	if !isManager(me) && !author {
		b.core.Fail(c, http.StatusForbidden, "只有团队所有者、管理员或书籍作者可以调整")
		return nil, nil, false
	}
	return t, &link, true
}

// UpdateBook PUT /teams/:id/books/:bookId {member_role} 调整普通成员在该书上的权限。
func (b *behavior) UpdateBook(c *gin.Context) {
	t, link, found := b.linkFor(c)
	if !found {
		return
	}
	var req bookPayload
	if c.ShouldBindJSON(&req) != nil || !bookRoles[req.MemberRole] {
		b.core.Fail(c, http.StatusBadRequest, "成员权限必须为编辑、建议或只读")
		return
	}
	b.core.Gorm().Model(link).Update("member_role", req.MemberRole)
	b.syncBook(t.ID, link.BookID)
	b.core.OK(c, link)
}

// RemoveBook DELETE /teams/:id/books/:bookId 把书移出团队，收回团队成员的权限。
func (b *behavior) RemoveBook(c *gin.Context) {
	t, link, found := b.linkFor(c)
	if !found {
		return
	}
	if err := b.core.Gorm().Delete(link).Error; err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "移出失败")
		return
	}
	b.syncBook(t.ID, link.BookID)
	b.core.OK(c, gin.H{"removed": true})
}

// BookTeam GET /team-books/:bookId 书籍所属的团队（书籍作者与团队成员可见），供书籍设置页展示与调整。
func (b *behavior) BookTeam(c *gin.Context) {
	bookID, valid := parseID(c, "bookId")
	if !valid {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	db := b.core.Gorm()
	u := b.core.CurrentUser(c)
	var book models.Book
	if db.Select("id", "user_id").First(&book, bookID).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "书籍不存在")
		return
	}
	var link Book
	if db.Where("book_id = ?", bookID).First(&link).Error != nil {
		b.core.OK(c, gin.H{"team": nil})
		return
	}
	var t Team
	var me Member
	member := db.Where("team_id = ? AND user_id = ? AND status = ?", link.TeamID, u.ID, "accepted").First(&me).Error == nil
	if db.First(&t, link.TeamID).Error != nil || (!member && book.UserID != u.ID && !b.core.IsAdmin(u)) {
		b.core.OK(c, gin.H{"team": nil})
		return
	}
	b.core.OK(c, gin.H{"team": gin.H{"id": t.ID, "name": t.Name, "slug": t.Slug}, "member_role": link.MemberRole,
		"can_change": book.UserID == u.ID || (member && isManager(&me))})
}

// —— 管理员 ——

// AdminList GET /admin/teams?q=&page= 站内全部团队（名称搜索），含所有者与成员、书籍数。
func (b *behavior) AdminList(c *gin.Context) {
	page, pageSize := b.core.Paginate(c)
	db := b.core.Gorm()
	q := db.Model(&Team{})
	if kw := strings.TrimSpace(c.Query("q")); kw != "" {
		q = q.Where("name LIKE ? OR slug LIKE ?", "%"+kw+"%", "%"+kw+"%")
	}
	var total int64
	q.Count(&total)
	var rows []Team
	q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows)
	owners := make([]uint, 0, len(rows))
	for _, t := range rows {
		owners = append(owners, t.OwnerID)
	}
	users := b.users(owners)
	items := make([]gin.H, 0, len(rows))
	for _, t := range rows {
		items = append(items, gin.H{"team": b.view(t, ""), "owner": users[t.OwnerID]})
	}
	b.core.OK(c, plugincore.PageResult{Items: items, Total: total, Page: page, PageSize: pageSize})
}

// AdminDelete DELETE /admin/teams/:id 解散团队（收回团队授予的书籍权限，书籍仍归各自作者）。
func (b *behavior) AdminDelete(c *gin.Context) {
	id, valid := parseID(c, "id")
	var t Team
	if !valid || b.core.Gorm().First(&t, id).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "团队不存在")
		return
	}
	if err := b.deleteTeam(&t); err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "解散失败")
		return
	}
	b.core.RecordAudit(c, "teams.deleted", "team", strconv.FormatUint(uint64(t.ID), 10), t.Name, nil)
	b.core.OK(c, gin.H{"deleted": true})
}
