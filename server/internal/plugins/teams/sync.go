package teams

import (
	"log"

	"knowforge/server/internal/models"
)

// —— 团队权限同步：把「团队成员 × 团队书籍」维护为协作者记录（team_id 为本团队）。——
// 所有者与管理员为 editor，普通成员为该书的 MemberRole；书籍作者本人不需要记录。
// 已有直接协作者记录（team_id = 0，待确认或已接受）的成员保持原样（直接邀请优先）；已拒绝的直接邀请改为团队权限。

// desiredRole 成员在团队书籍上应有的协作者角色。
func desiredRole(memberRole, bookMemberRole string) string {
	if memberRole == RoleOwner || memberRole == RoleAdmin {
		return "editor"
	}
	return bookMemberRole
}

// syncBook 按团队当前的成员与书籍设置同步一本书的团队权限；书籍已移出团队时清除。
func (b *behavior) syncBook(teamID, bookID uint) {
	db := b.core.Gorm()
	var link Book
	if db.Where("team_id = ? AND book_id = ?", teamID, bookID).First(&link).Error != nil || !b.core.PluginEnabled(pluginKey) {
		db.Where("book_id = ? AND team_id = ?", bookID, teamID).Delete(&models.BookCollaborator{})
		return
	}
	var book models.Book
	if db.Select("id", "user_id").First(&book, bookID).Error != nil {
		return
	}
	var members []Member
	db.Where("team_id = ? AND status = ?", teamID, "accepted").Find(&members)
	want := map[uint]string{}
	for _, m := range members {
		if m.UserID != book.UserID {
			want[m.UserID] = desiredRole(m.Role, link.MemberRole)
		}
	}
	var rows []models.BookCollaborator
	db.Where("book_id = ?", bookID).Find(&rows)
	for _, r := range rows {
		role, wanted := want[r.UserID]
		switch {
		case r.TeamID == teamID && !wanted:
			db.Delete(&r)
		case r.TeamID == teamID:
			if r.Role != role || r.Status != "accepted" {
				db.Model(&r).Updates(map[string]any{"role": role, "status": "accepted"})
			}
			delete(want, r.UserID)
		case wanted && r.TeamID == 0 && r.Status == "rejected":
			db.Model(&r).Updates(map[string]any{"role": role, "status": "accepted", "team_id": teamID, "invited_by": link.AddedBy})
			delete(want, r.UserID)
		case wanted:
			delete(want, r.UserID) // 直接协作者（或其他团队的遗留记录）优先
		}
	}
	for userID, role := range want {
		row := models.BookCollaborator{BookID: bookID, UserID: userID, Role: role, Status: "accepted", InvitedBy: link.AddedBy, TeamID: teamID}
		if err := db.Create(&row).Error; err != nil {
			log.Printf("团队空间：同步书籍 %d 的成员 %d 失败: %v", bookID, userID, err)
		}
	}
}

// syncTeam 同步团队全部书籍；并清除已不属于本团队的书籍上的团队权限。
func (b *behavior) syncTeam(teamID uint) {
	db := b.core.Gorm()
	var bookIDs []uint
	db.Model(&Book{}).Where("team_id = ?", teamID).Pluck("book_id", &bookIDs)
	for _, id := range bookIDs {
		b.syncBook(teamID, id)
	}
	q := db.Where("team_id = ?", teamID)
	if len(bookIDs) > 0 {
		q = q.Where("book_id NOT IN ?", bookIDs)
	}
	q.Delete(&models.BookCollaborator{})
}
