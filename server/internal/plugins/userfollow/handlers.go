package userfollow

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

const maxPageSize = 50

type behavior struct {
	core     plugincore.Core
	hookOnce sync.Once // 章节发布钩子只注册一次（RegisterRoutes 可能被多次调用）
}

func (b *behavior) Key() string { return pluginKey }

func (b *behavior) RegisterRoutes(api *gin.RouterGroup, core plugincore.Core) {
	b.core = core
	guard := core.RequireFeaturePlugin(pluginKey)
	api.POST("/users/:username/follow", core.RequireAuth(), guard, core.RequirePermission(PermCreate), core.RateLimitReaction(), b.Follow)
	api.DELETE("/users/:username/follow", core.RequireAuth(), guard, core.RequirePermission(PermDelete), core.RateLimitReaction(), b.Unfollow)
	api.GET("/users/:username/follow-stats", core.OptionalAuth(), guard, b.Stats)
	api.GET("/users/:username/followers", core.OptionalAuth(), guard, b.Followers)
	api.GET("/users/:username/following", core.OptionalAuth(), guard, b.Following)
	api.GET("/users/me/followed-authors/books", core.RequireAuth(), guard, b.FollowedAuthorBooks)
	b.hookOnce.Do(func() {
		plugincore.OnChapterPublished(func(c plugincore.Core, book *models.Book, doc *models.Document) {
			(&behavior{core: c}).notifyChapterPublished(book, doc)
		})
	})
}

// publicUser 关注列表中的用户。
type publicUser struct {
	ID          uint   `json:"id"`
	Username    string `json:"username"`
	Nickname    string `json:"nickname"`
	DisplayName string `json:"display_name"`
	Avatar      string `json:"avatar"`
	Bio         string `json:"bio"`
}

func toPublicUser(u *models.User) publicUser {
	return publicUser{ID: u.ID, Username: u.Username, Nickname: u.Nickname, DisplayName: u.PublicName(), Avatar: u.Avatar, Bio: u.Bio}
}

// findUser 按用户名查找启用中的用户，找不到时返回 404。
func (b *behavior) findUser(c *gin.Context) *models.User {
	var u models.User
	if err := b.core.Gorm().Where("username = ? AND is_active = ?", strings.TrimSpace(c.Param("username")), true).First(&u).Error; err != nil {
		b.core.Fail(c, http.StatusNotFound, "用户不存在")
		return nil
	}
	return &u
}

// counts 关注者数与关注数（只计启用中的用户）。
func (b *behavior) counts(userID uint) (followers, following int64) {
	db := b.core.Gorm()
	db.Model(&UserFollow{}).Joins("JOIN users u ON u.id = user_follows.user_id AND u.is_active = ?", true).
		Where("user_follows.followee_id = ?", userID).Count(&followers)
	db.Model(&UserFollow{}).Joins("JOIN users u ON u.id = user_follows.followee_id AND u.is_active = ?", true).
		Where("user_follows.user_id = ?", userID).Count(&following)
	return
}

func (b *behavior) follows(followerID, followeeID uint) bool {
	var n int64
	b.core.Gorm().Model(&UserFollow{}).Where("user_id = ? AND followee_id = ?", followerID, followeeID).Count(&n)
	return n > 0
}

func (b *behavior) stats(target *models.User, viewer *models.User) gin.H {
	followers, following := b.counts(target.ID)
	out := gin.H{"followers": followers, "following": following, "followed_by_me": false, "follows_me": false, "is_me": false}
	if viewer != nil {
		out["is_me"] = viewer.ID == target.ID
		if viewer.ID != target.ID {
			out["followed_by_me"] = b.follows(viewer.ID, target.ID)
			out["follows_me"] = b.follows(target.ID, viewer.ID)
		}
	}
	return out
}

// Follow POST /users/:username/follow 关注作者（幂等）；首次关注时通知对方。
func (b *behavior) Follow(c *gin.Context) {
	me := b.core.CurrentUser(c)
	target := b.findUser(c)
	if target == nil {
		return
	}
	if target.ID == me.ID {
		b.core.Fail(c, http.StatusBadRequest, "不能关注自己")
		return
	}
	f := UserFollow{UserID: me.ID, FolloweeID: target.ID}
	res := b.core.Gorm().Where("user_id = ? AND followee_id = ?", me.ID, target.ID).FirstOrCreate(&f)
	if res.Error != nil {
		b.core.Fail(c, http.StatusInternalServerError, "关注失败")
		return
	}
	if res.RowsAffected > 0 && b.wantsNotice(target.ID) {
		b.core.NotifyI18n(target.ID, notifyType, "notify.userFollow.followed", map[string]string{"user": me.PublicName()},
			map[string]any{"link": "/user/" + me.Username, "follower_id": me.ID})
	}
	b.core.OK(c, b.stats(target, me))
}

// Unfollow DELETE /users/:username/follow 取消关注。
func (b *behavior) Unfollow(c *gin.Context) {
	me := b.core.CurrentUser(c)
	target := b.findUser(c)
	if target == nil {
		return
	}
	b.core.Gorm().Where("user_id = ? AND followee_id = ?", me.ID, target.ID).Delete(&UserFollow{})
	b.core.OK(c, b.stats(target, me))
}

// Stats GET /users/:username/follow-stats 关注者数、关注数；登录时附带双方关注状态。
func (b *behavior) Stats(c *gin.Context) {
	target := b.findUser(c)
	if target == nil {
		return
	}
	b.core.OK(c, b.stats(target, b.core.CurrentUser(c)))
}

// Followers GET /users/:username/followers?page=&page_size= 关注该用户的人（最近关注在前）。
func (b *behavior) Followers(c *gin.Context) { b.list(c, "followee_id", "user_id") }

// Following GET /users/:username/following?page=&page_size= 该用户关注的人（最近关注在前）。
func (b *behavior) Following(c *gin.Context) { b.list(c, "user_id", "followee_id") }

// list 分页列出关系另一端的用户：by 为目标用户所在列，other 为要列出的用户列。
// 登录时每项附带当前用户是否已关注（followed_by_me）。
func (b *behavior) list(c *gin.Context, by, other string) {
	target := b.findUser(c)
	if target == nil {
		return
	}
	page, pageSize := b.core.Paginate(c)
	pageSize = min(pageSize, maxPageSize)
	q := b.core.Gorm().Model(&UserFollow{}).
		Joins(fmt.Sprintf("JOIN users u ON u.id = user_follows.%s AND u.is_active = ?", other), true).
		Where(fmt.Sprintf("user_follows.%s = ?", by), target.ID)
	var total int64
	q.Session(&gorm.Session{}).Count(&total)
	var rows []UserFollow
	q.Select("user_follows.*").Order("user_follows.created_at DESC, user_follows.id DESC").
		Limit(pageSize).Offset((page - 1) * pageSize).Find(&rows)
	ids := make([]uint, 0, len(rows))
	for _, r := range rows {
		if other == "user_id" {
			ids = append(ids, r.UserID)
		} else {
			ids = append(ids, r.FolloweeID)
		}
	}
	users := map[uint]*models.User{}
	if len(ids) > 0 {
		var list []models.User
		b.core.Gorm().Where("id IN ?", ids).Find(&list)
		for i := range list {
			users[list[i].ID] = &list[i]
		}
	}
	mine := map[uint]bool{}
	viewer := b.core.CurrentUser(c)
	if viewer != nil && len(ids) > 0 {
		var followed []uint
		b.core.Gorm().Model(&UserFollow{}).Where("user_id = ? AND followee_id IN ?", viewer.ID, ids).Pluck("followee_id", &followed)
		for _, id := range followed {
			mine[id] = true
		}
	}
	items := make([]gin.H, 0, len(rows))
	for i, r := range rows {
		u, ok := users[ids[i]]
		if !ok {
			continue
		}
		items = append(items, gin.H{"user": toPublicUser(u), "followed_at": r.CreatedAt, "followed_by_me": mine[u.ID], "is_me": viewer != nil && viewer.ID == u.ID})
	}
	b.core.OK(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

// FollowedAuthorBooks GET /users/me/followed-authors/books?page=&page_size= 关注作者的公开书籍，最近更新在前。
func (b *behavior) FollowedAuthorBooks(c *gin.Context) {
	me := b.core.CurrentUser(c)
	page, pageSize := b.core.Paginate(c)
	pageSize = min(pageSize, maxPageSize)
	authors := b.core.Gorm().Model(&UserFollow{}).Select("followee_id").Where("user_id = ?", me.ID)
	q := b.core.Gorm().Model(&models.Book{}).
		Where("user_id IN (?) AND is_public = ? AND status IN ?", authors, true, b.core.PubliclyReadableBookStatuses())
	var total int64
	q.Session(&gorm.Session{}).Count(&total)
	books := []models.Book{}
	b.core.PreloadBookUserOn(q).Order("updated_at DESC, id DESC").Limit(pageSize).Offset((page - 1) * pageSize).Find(&books)
	b.core.AttachChapterCounts(books)
	b.core.DecorateBookList(books)
	b.core.OK(c, gin.H{"items": books, "total": total, "page": page, "page_size": pageSize})
}

// wantsNotice 用户是否接收关注动态通知（无偏好记录 = 默认接收）。
func (b *behavior) wantsNotice(userID uint) bool {
	var p models.UserNotificationPref
	if b.core.Gorm().Where("user_id = ?", userID).First(&p).Error != nil {
		return true
	}
	return p.Social
}

// notifyChapterPublished 公开书籍发布新章节时通知作者的关注者；已关注该书的读者由「书籍关注」通知，不重复发送。
func (b *behavior) notifyChapterPublished(book *models.Book, doc *models.Document) {
	core := b.core
	if book == nil || doc == nil || !core.PluginEnabled(pluginKey) || !book.IsPublic ||
		!slices.Contains(core.PubliclyReadableBookStatuses(), book.Status) {
		return
	}
	db := core.Gorm()
	q := db.Model(&UserFollow{}).Where("followee_id = ? AND user_id <> ?", book.UserID, book.UserID)
	if core.PluginEnabled(bookFollowKey) && db.Migrator().HasTable("book_follows") {
		q = q.Where("NOT EXISTS (SELECT 1 FROM book_follows bf WHERE bf.user_id = user_follows.user_id AND bf.book_id = ?)", book.ID)
	}
	var followers []uint
	q.Pluck("user_id", &followers)
	if len(followers) == 0 {
		return
	}
	off := map[uint]bool{}
	var prefs []models.UserNotificationPref
	db.Where("user_id IN ?", followers).Find(&prefs)
	for _, p := range prefs {
		if !p.Social {
			off[p.UserID] = true
		}
	}
	var author models.User
	if db.Select("id, username, nickname, name_display").First(&author, book.UserID).Error != nil {
		return
	}
	params := map[string]string{"user": author.PublicName(), "book": book.Title, "chapter": doc.Title}
	payload := map[string]any{"link": fmt.Sprintf("/book/reader/%s/%s", book.Slug, doc.Slug), "book_id": book.ID}
	for _, uid := range followers {
		if !off[uid] {
			core.NotifyI18n(uid, notifyType, "notify.userFollow.chapterPublished", params, payload)
		}
	}
}
