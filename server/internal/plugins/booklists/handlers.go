package booklists

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

func (b *behavior) RegisterRoutes(api *gin.RouterGroup, core plugincore.Core) {
	b.core = core
	feat := core.RequireFeaturePlugin(pluginKey)
	use := func(h gin.HandlerFunc) []gin.HandlerFunc {
		return []gin.HandlerFunc{core.RequireAuth(), feat, core.RequirePermissionMiddleware(PermUse), h}
	}
	read := func(h gin.HandlerFunc) []gin.HandlerFunc { return []gin.HandlerFunc{core.OptionalAuth(), feat, h} }
	api.GET("/book-lists", read(b.Discover)...)
	api.GET("/book-lists/mine", use(b.Mine)...)
	api.GET("/book-lists/followed", use(b.Followed)...)
	api.GET("/users/:username/book-lists", read(b.UserLists)...)
	api.GET("/books/:id/book-lists", read(b.BookLists)...)
	api.POST("/book-lists", use(b.Create)...)
	api.GET("/book-lists/:id", read(b.Detail)...)
	api.PUT("/book-lists/:id", use(b.Update)...)
	api.DELETE("/book-lists/:id", use(b.Delete)...)
	api.POST("/book-lists/:id/items", use(b.AddItem)...)
	api.PUT("/book-lists/:id/items/:bookId", use(b.UpdateItem)...)
	api.DELETE("/book-lists/:id/items/:bookId", use(b.RemoveItem)...)
	api.PUT("/book-lists/:id/order", use(b.Reorder)...)
	api.POST("/book-lists/:id/follow", use(b.FollowList)...)
	api.DELETE("/book-lists/:id/follow", use(b.UnfollowList)...)
}

// —— 视图 ——

type owner struct {
	ID          uint   `json:"id"`
	Username    string `json:"username"`
	Nickname    string `json:"nickname"`
	DisplayName string `json:"display_name"`
	Avatar      string `json:"avatar"`
}

type cover struct {
	BookID uint   `json:"book_id"`
	Title  string `json:"title"`
	Cover  string `json:"cover_image"`
}

// listView 书单卡片（列表用）：Covers 为前几本查看者可读书籍的封面。
type listView struct {
	List
	Owner     *owner  `json:"owner"`
	Covers    []cover `json:"covers"`
	Following bool    `json:"following"`
	Contains  *bool   `json:"contains,omitempty"`
}

// itemView 书单中的一本书。
type itemView struct {
	Book      models.Book `json:"book"`
	Note      string      `json:"note"`
	SortOrder int         `json:"sort_order"`
	AddedAt   string      `json:"added_at"`
}

func (b *behavior) visible(u *models.User, l *List) bool {
	return l.IsPublic || (u != nil && (u.ID == l.UserID || b.core.IsAdmin(u)))
}

// readableBooks 按 id 加载书籍并过滤掉查看者不可读的（保持 ids 顺序）。
func (b *behavior) readableBooks(u *models.User, ids []uint) map[uint]*models.Book {
	out := map[uint]*models.Book{}
	if len(ids) == 0 {
		return out
	}
	var books []models.Book
	b.core.PreloadBookUser().Where("id IN ?", ids).Find(&books)
	for i := range books {
		if b.core.CanReadBook(u, &books[i]) {
			out[books[i].ID] = &books[i]
		}
	}
	return out
}

// views 组装书单卡片：所有者、前几本可读书籍的封面、当前用户是否已收藏。
func (b *behavior) views(u *models.User, lists []List) []listView {
	out := make([]listView, 0, len(lists))
	if len(lists) == 0 {
		return out
	}
	db := b.core.Gorm()
	listIDs, userIDs := []uint{}, []uint{}
	for _, l := range lists {
		listIDs = append(listIDs, l.ID)
		userIDs = append(userIDs, l.UserID)
	}
	var users []models.User
	db.Where("id IN ?", userIDs).Find(&users)
	owners := map[uint]*owner{}
	for _, x := range users {
		owners[x.ID] = &owner{ID: x.ID, Username: x.Username, Nickname: x.Nickname, DisplayName: x.PublicName(), Avatar: x.Avatar}
	}
	var items []Item
	db.Where("list_id IN ?", listIDs).Order("sort_order ASC, id ASC").Find(&items)
	bookIDs := []uint{}
	for _, it := range items {
		bookIDs = append(bookIDs, it.BookID)
	}
	books := b.readableBooks(u, bookIDs)
	covers := map[uint][]cover{}
	for _, it := range items {
		if bk := books[it.BookID]; bk != nil && len(covers[it.ListID]) < coverSamples {
			covers[it.ListID] = append(covers[it.ListID], cover{BookID: bk.ID, Title: bk.Title, Cover: bk.CoverImage})
		}
	}
	following := map[uint]bool{}
	if u != nil {
		var ids []uint
		db.Model(&Follow{}).Where("user_id = ? AND list_id IN ?", u.ID, listIDs).Pluck("list_id", &ids)
		for _, id := range ids {
			following[id] = true
		}
	}
	for _, l := range lists {
		cs := covers[l.ID]
		if cs == nil {
			cs = []cover{}
		}
		out = append(out, listView{List: l, Owner: owners[l.UserID], Covers: cs, Following: following[l.ID]})
	}
	return out
}

func (b *behavior) page(c *gin.Context, q *gorm.DB, u *models.User) {
	page, size := b.core.Paginate(c)
	var total int64
	q.Model(&List{}).Count(&total)
	var lists []List
	q.Offset((page - 1) * size).Limit(size).Find(&lists)
	b.core.OK(c, plugincore.PageResult{Items: b.views(u, lists), Total: total, Page: page, PageSize: size})
}

// Discover GET /book-lists?sort=popular|latest 书单广场：有书的公开书单。
func (b *behavior) Discover(c *gin.Context) {
	q := b.core.Gorm().Where("is_public = ? AND item_count > 0", true)
	if c.Query("sort") == "latest" {
		q = q.Order("updated_at DESC, id DESC")
	} else {
		q = q.Order("follower_count DESC, updated_at DESC, id DESC")
	}
	b.page(c, q, b.core.CurrentUser(c))
}

// Mine GET /book-lists/mine?book_id= 我的书单；带 book_id 时标注每个书单是否已收录该书（「加入书单」弹窗用）。
func (b *behavior) Mine(c *gin.Context) {
	u := b.core.CurrentUser(c)
	var lists []List
	b.core.Gorm().Where("user_id = ?", u.ID).Order("updated_at DESC, id DESC").Find(&lists)
	views := b.views(u, lists)
	if bookID, _ := strconv.Atoi(c.Query("book_id")); bookID > 0 && len(lists) > 0 {
		var in []uint
		ids := []uint{}
		for _, l := range lists {
			ids = append(ids, l.ID)
		}
		b.core.Gorm().Model(&Item{}).Where("book_id = ? AND list_id IN ?", bookID, ids).Pluck("list_id", &in)
		has := map[uint]bool{}
		for _, id := range in {
			has[id] = true
		}
		for i := range views {
			v := has[views[i].ID]
			views[i].Contains = &v
		}
	}
	b.core.OK(c, gin.H{"items": views, "limit": plugincore.EntitlementValue(b.core, u, entMax)})
}

// Followed GET /book-lists/followed 我收藏的书单（已被设为私有的不显示）。
func (b *behavior) Followed(c *gin.Context) {
	u := b.core.CurrentUser(c)
	sub := b.core.Gorm().Model(&Follow{}).Select("list_id").Where("user_id = ?", u.ID)
	q := b.core.Gorm().Where("id IN (?) AND (is_public = ? OR user_id = ?)", sub, true, u.ID).Order("updated_at DESC, id DESC")
	b.page(c, q, u)
}

// UserLists GET /users/:username/book-lists 某用户的公开书单（本人可见全部）。
func (b *behavior) UserLists(c *gin.Context) {
	var target models.User
	if b.core.Gorm().Where("username = ? AND is_active = ?", c.Param("username"), true).First(&target).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "用户不存在")
		return
	}
	u := b.core.CurrentUser(c)
	q := b.core.Gorm().Where("user_id = ?", target.ID)
	if u == nil || u.ID != target.ID {
		q = q.Where("is_public = ?", true)
	}
	b.page(c, q.Order("updated_at DESC, id DESC"), u)
}

// BookLists GET /books/:id/book-lists 收录这本书的公开书单（最多 6 个，收藏多的在前）与总数。
func (b *behavior) BookLists(c *gin.Context) {
	book, _ := b.core.FindBook(c)
	u := b.core.CurrentUser(c)
	if book == nil || !b.core.CanReadBook(u, book) {
		b.core.Fail(c, http.StatusNotFound, "书籍不存在")
		return
	}
	sub := b.core.Gorm().Model(&Item{}).Select("list_id").Where("book_id = ?", book.ID)
	q := b.core.Gorm().Model(&List{}).Where("id IN (?) AND is_public = ?", sub, true)
	var total int64
	q.Count(&total)
	var lists []List
	q.Order("follower_count DESC, updated_at DESC, id DESC").Limit(6).Find(&lists)
	b.core.OK(c, gin.H{"items": b.views(u, lists), "total": total})
}

// —— 书单 ——

type listRequest struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	IsPublic    *bool   `json:"is_public"`
}

func (b *behavior) apply(c *gin.Context, l *List, req listRequest) bool {
	if req.Title != nil {
		t := strings.TrimSpace(*req.Title)
		if t == "" || utf8.RuneCountInString(t) > maxTitle {
			b.core.Fail(c, http.StatusBadRequest, "书单名称为 1-60 个字")
			return false
		}
		l.Title = t
	}
	if req.Description != nil {
		d := strings.TrimSpace(*req.Description)
		if utf8.RuneCountInString(d) > maxDesc {
			b.core.Fail(c, http.StatusBadRequest, "书单简介最多 1000 字")
			return false
		}
		l.Description = d
	}
	if req.IsPublic != nil {
		l.IsPublic = *req.IsPublic
	}
	return true
}

// Create POST /book-lists {title, description?, is_public?, book_id?} 创建书单（可同时收录一本书）。
func (b *behavior) Create(c *gin.Context) {
	var req struct {
		listRequest
		BookID uint `json:"book_id"`
	}
	if c.ShouldBindJSON(&req) != nil || req.Title == nil {
		b.core.Fail(c, http.StatusBadRequest, "请填写书单名称")
		return
	}
	u := b.core.CurrentUser(c)
	if limit := plugincore.EntitlementValue(b.core, u, entMax); limit != plugincore.Unlimited {
		var n int64
		b.core.Gorm().Model(&List{}).Where("user_id = ?", u.ID).Count(&n)
		if n >= limit {
			b.core.Fail(c, http.StatusForbidden, "书单数量已达上限，可删除不用的书单，或升级等级、开通会员获得更多")
			return
		}
	}
	l := List{UserID: u.ID, IsPublic: true}
	if !b.apply(c, &l, req.listRequest) {
		return
	}
	var book *models.Book
	if req.BookID > 0 {
		if book = b.addableBook(c, u, req.BookID); book == nil {
			return
		}
	}
	err := b.core.Gorm().Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&l).Error; err != nil {
			return err
		}
		if book != nil {
			l.ItemCount = 1
			if err := tx.Create(&Item{ListID: l.ID, BookID: book.ID}).Error; err != nil {
				return err
			}
			return tx.Model(&l).Update("item_count", 1).Error
		}
		return nil
	})
	if err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "创建失败")
		return
	}
	b.core.OK(c, b.views(u, []List{l})[0])
}

// myList 当前用户自己的书单。
func (b *behavior) myList(c *gin.Context) (*List, *models.User, bool) {
	u := b.core.CurrentUser(c)
	var l List
	if b.core.Gorm().First(&l, c.Param("id")).Error != nil || (!b.visible(u, &l)) {
		b.core.Fail(c, http.StatusNotFound, "书单不存在")
		return nil, u, false
	}
	if l.UserID != u.ID {
		b.core.Fail(c, http.StatusForbidden, "只能修改自己的书单")
		return nil, u, false
	}
	return &l, u, true
}

// Detail GET /book-lists/:id 书单详情：只列出查看者可读的书籍；hidden 为不可读而未显示的数量（仅创建者可见）。
func (b *behavior) Detail(c *gin.Context) {
	u := b.core.CurrentUser(c)
	var l List
	if b.core.Gorm().First(&l, c.Param("id")).Error != nil || !b.visible(u, &l) {
		b.core.Fail(c, http.StatusNotFound, "书单不存在")
		return
	}
	var items []Item
	b.core.Gorm().Where("list_id = ?", l.ID).Order("sort_order ASC, id ASC").Find(&items)
	ids := []uint{}
	for _, it := range items {
		ids = append(ids, it.BookID)
	}
	books := b.readableBooks(u, ids)
	ordered := []models.Book{}
	for _, it := range items {
		if bk := books[it.BookID]; bk != nil {
			ordered = append(ordered, *bk)
		}
	}
	b.core.AttachChapterCounts(ordered)
	b.core.DecorateBookList(ordered)
	byID := map[uint]models.Book{}
	for _, bk := range ordered {
		byID[bk.ID] = bk
	}
	views := []itemView{}
	for _, it := range items {
		if bk, ok := byID[it.BookID]; ok {
			views = append(views, itemView{Book: bk, Note: it.Note, SortOrder: it.SortOrder, AddedAt: it.CreatedAt.Format("2006-01-02T15:04:05Z07:00")})
		}
	}
	mine := u != nil && u.ID == l.UserID
	hidden := 0
	if mine {
		hidden = len(items) - len(views)
	}
	b.core.OK(c, gin.H{"list": b.views(u, []List{l})[0], "items": views, "mine": mine, "hidden": hidden})
}

// Update PUT /book-lists/:id {title?, description?, is_public?}
func (b *behavior) Update(c *gin.Context) {
	l, u, ok := b.myList(c)
	if !ok {
		return
	}
	var req listRequest
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if !b.apply(c, l, req) {
		return
	}
	b.core.Gorm().Model(l).Select("title", "description", "is_public").Updates(l)
	b.core.OK(c, b.views(u, []List{*l})[0])
}

// Delete DELETE /book-lists/:id 删除书单及其收录与收藏记录。
func (b *behavior) Delete(c *gin.Context) {
	l, _, ok := b.myList(c)
	if !ok {
		return
	}
	_ = b.core.Gorm().Transaction(func(tx *gorm.DB) error {
		tx.Where("list_id = ?", l.ID).Delete(&Item{})
		tx.Where("list_id = ?", l.ID).Delete(&Follow{})
		return tx.Delete(l).Error
	})
	b.core.OK(c, gin.H{"deleted": true})
}

// —— 收录 ——

// addableBook 可收录的书：当前用户可读即可（书单公开后，其他人只看得到他们可读的书）。
func (b *behavior) addableBook(c *gin.Context, u *models.User, id uint) *models.Book {
	var book models.Book
	if b.core.Gorm().First(&book, id).Error != nil || !b.core.CanReadBook(u, &book) {
		b.core.Fail(c, http.StatusNotFound, "书籍不存在")
		return nil
	}
	return &book
}

// recount 重新统计书单收录数并刷新更新时间。
func (b *behavior) recount(tx *gorm.DB, l *List) error {
	var n int64
	tx.Model(&Item{}).Where("list_id = ?", l.ID).Count(&n)
	l.ItemCount = int(n)
	return tx.Model(l).Updates(map[string]any{"item_count": l.ItemCount, "updated_at": gorm.Expr("CURRENT_TIMESTAMP")}).Error
}

func cleanNote(s string) (string, bool) {
	s = strings.TrimSpace(s)
	return s, utf8.RuneCountInString(s) <= maxNote
}

// AddItem POST /book-lists/:id/items {book_id, note?} 收录一本书（排在最后；已收录则返回冲突）。
func (b *behavior) AddItem(c *gin.Context) {
	l, u, ok := b.myList(c)
	if !ok {
		return
	}
	var req struct {
		BookID uint   `json:"book_id"`
		Note   string `json:"note"`
	}
	if c.ShouldBindJSON(&req) != nil || req.BookID == 0 {
		b.core.Fail(c, http.StatusBadRequest, "请选择书籍")
		return
	}
	note, valid := cleanNote(req.Note)
	if !valid {
		b.core.Fail(c, http.StatusBadRequest, "推荐语最多 300 字")
		return
	}
	book := b.addableBook(c, u, req.BookID)
	if book == nil {
		return
	}
	errFull := errors.New("full")
	errExists := errors.New("exists")
	err := b.core.Gorm().Transaction(func(tx *gorm.DB) error {
		// 锁住书单行，避免并发收录超出上限、排序号重复
		var locked List
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, l.ID).Error; err != nil {
			return err
		}
		var n int64
		tx.Model(&Item{}).Where("list_id = ?", l.ID).Count(&n)
		if n >= maxItems {
			return errFull
		}
		var exists int64
		tx.Model(&Item{}).Where("list_id = ? AND book_id = ?", l.ID, book.ID).Count(&exists)
		if exists > 0 {
			return errExists
		}
		var last Item
		order := 0
		if tx.Where("list_id = ?", l.ID).Order("sort_order DESC").First(&last).Error == nil {
			order = last.SortOrder + 1
		}
		if err := tx.Create(&Item{ListID: l.ID, BookID: book.ID, Note: note, SortOrder: order}).Error; err != nil {
			return err
		}
		return b.recount(tx, l)
	})
	switch {
	case errors.Is(err, errFull):
		b.core.Fail(c, http.StatusBadRequest, "每个书单最多收录 500 本书")
	case errors.Is(err, errExists):
		b.core.Fail(c, http.StatusConflict, "这本书已在书单中")
	case err != nil:
		b.core.Fail(c, http.StatusInternalServerError, "收录失败")
	default:
		b.core.OK(c, gin.H{"item_count": l.ItemCount})
	}
}

// UpdateItem PUT /book-lists/:id/items/:bookId {note} 修改推荐语。
func (b *behavior) UpdateItem(c *gin.Context) {
	l, _, ok := b.myList(c)
	if !ok {
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&req)
	note, valid := cleanNote(req.Note)
	if !valid {
		b.core.Fail(c, http.StatusBadRequest, "推荐语最多 300 字")
		return
	}
	res := b.core.Gorm().Model(&Item{}).Where("list_id = ? AND book_id = ?", l.ID, c.Param("bookId")).Update("note", note)
	if res.RowsAffected == 0 {
		b.core.Fail(c, http.StatusNotFound, "这本书不在书单中")
		return
	}
	b.core.OK(c, gin.H{"note": note})
}

// RemoveItem DELETE /book-lists/:id/items/:bookId 移出一本书。
func (b *behavior) RemoveItem(c *gin.Context) {
	l, _, ok := b.myList(c)
	if !ok {
		return
	}
	err := b.core.Gorm().Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("list_id = ? AND book_id = ?", l.ID, c.Param("bookId")).Delete(&Item{}).Error; err != nil {
			return err
		}
		return b.recount(tx, l)
	})
	if err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "移出失败")
		return
	}
	b.core.OK(c, gin.H{"item_count": l.ItemCount})
}

// Reorder PUT /book-lists/:id/order {book_ids[]} 按给定顺序排列（未列出的排在后面，保持原有相对顺序）。
func (b *behavior) Reorder(c *gin.Context) {
	l, _, ok := b.myList(c)
	if !ok {
		return
	}
	var req struct {
		BookIDs []uint `json:"book_ids"`
	}
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	err := b.core.Gorm().Transaction(func(tx *gorm.DB) error {
		var items []Item
		tx.Where("list_id = ?", l.ID).Order("sort_order ASC, id ASC").Find(&items)
		rank := map[uint]int{}
		for i, id := range req.BookIDs {
			if _, dup := rank[id]; !dup {
				rank[id] = i
			}
		}
		next := len(req.BookIDs)
		for _, it := range items {
			order, listed := rank[it.BookID]
			if !listed {
				order = next
				next++
			}
			if order != it.SortOrder {
				if err := tx.Model(&Item{}).Where("id = ?", it.ID).Update("sort_order", order).Error; err != nil {
					return err
				}
			}
		}
		return b.recount(tx, l)
	})
	if err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "保存顺序失败")
		return
	}
	b.core.OK(c, gin.H{"ok": true})
}

// —— 收藏 ——

// followable 可收藏的书单：他人的公开书单。
func (b *behavior) followable(c *gin.Context) (*List, *models.User, bool) {
	u := b.core.CurrentUser(c)
	var l List
	if b.core.Gorm().First(&l, c.Param("id")).Error != nil || !b.visible(u, &l) {
		b.core.Fail(c, http.StatusNotFound, "书单不存在")
		return nil, u, false
	}
	return &l, u, true
}

func (b *behavior) syncFollowers(tx *gorm.DB, l *List) error {
	var n int64
	tx.Model(&Follow{}).Where("list_id = ?", l.ID).Count(&n)
	l.FollowerCount = int(n)
	return tx.Model(l).UpdateColumn("follower_count", l.FollowerCount).Error
}

// FollowList POST /book-lists/:id/follow 收藏书单（幂等；不能收藏自己的书单）。
func (b *behavior) FollowList(c *gin.Context) {
	l, u, ok := b.followable(c)
	if !ok {
		return
	}
	if l.UserID == u.ID {
		b.core.Fail(c, http.StatusBadRequest, "不能收藏自己的书单")
		return
	}
	err := b.core.Gorm().Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&Follow{ListID: l.ID, UserID: u.ID}).Error; err != nil {
			return err
		}
		return b.syncFollowers(tx, l)
	})
	if err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "收藏失败")
		return
	}
	b.core.OK(c, gin.H{"following": true, "follower_count": l.FollowerCount})
}

// UnfollowList DELETE /book-lists/:id/follow 取消收藏（幂等）。
func (b *behavior) UnfollowList(c *gin.Context) {
	l, u, ok := b.followable(c)
	if !ok {
		return
	}
	err := b.core.Gorm().Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("list_id = ? AND user_id = ?", l.ID, u.ID).Delete(&Follow{}).Error; err != nil {
			return err
		}
		return b.syncFollowers(tx, l)
	})
	if err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "取消收藏失败")
		return
	}
	b.core.OK(c, gin.H{"following": false, "follower_count": l.FollowerCount})
}
