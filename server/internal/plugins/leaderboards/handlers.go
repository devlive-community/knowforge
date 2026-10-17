package leaderboards

import (
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

const maxPageSize = 50

func sortStable[T any](list []T, less func(a, b T) bool) {
	sort.SliceStable(list, func(i, j int) bool { return less(list[i], list[j]) })
}

func (b *behavior) RegisterRoutes(api *gin.RouterGroup, core plugincore.Core) {
	b.core = core
	guard := core.RequireFeaturePlugin(pluginKey)
	api.GET("/leaderboards", guard, b.List)
	api.GET("/leaderboards/:key", core.OptionalAuth(), guard, b.Show)
	admin := []gin.HandlerFunc{core.RequireAuth(), core.RequireAdmin(), guard, core.RequirePermissionMiddleware(PermManage)}
	api.GET("/admin/leaderboards", append(admin, b.AdminList)...)
	api.PUT("/admin/leaderboards", append(admin, b.AdminUpdate)...)
}

type boardMeta struct {
	Key     string   `json:"key"`
	Kind    string   `json:"kind"`
	Icon    string   `json:"icon"`
	Periods []string `json:"periods"`
}

func metaOf(l plugincore.Leaderboard) boardMeta {
	periods := l.Periods
	if len(periods) == 0 {
		periods = []string{plugincore.LeaderboardAll}
	}
	return boardMeta{Key: l.Key, Kind: l.Kind, Icon: l.Icon, Periods: periods}
}

// List GET /leaderboards 当前开放的榜单（按管理员设置的顺序）。
func (b *behavior) List(c *gin.Context) {
	items := []boardMeta{}
	for _, l := range b.boards() {
		items = append(items, metaOf(l))
	}
	b.core.OK(c, gin.H{"items": items})
}

// ranked 周期内得分 > 0 且对象公开的排名子查询（列 subject_id、value）。
func (b *behavior) ranked(l plugincore.Leaderboard, since *time.Time) *gorm.DB {
	db := b.core.Gorm()
	q := db.Table("(?) AS s", l.Scores(b.core, since)).Where("s.value > 0")
	if l.Kind == plugincore.LeaderboardBooks {
		return q.Joins("JOIN books bk ON bk.id = s.subject_id AND bk.deleted_at IS NULL AND bk.is_public = ? AND bk.status IN ?", true, b.core.PubliclyReadableBookStatuses())
	}
	return q.Joins("JOIN users u ON u.id = s.subject_id AND u.is_active = ?", true)
}

type scoreRow struct {
	SubjectID uint
	Value     int64
}

type entryUser struct {
	ID          uint   `json:"id"`
	Username    string `json:"username"`
	Nickname    string `json:"nickname"`
	DisplayName string `json:"display_name"`
	Avatar      string `json:"avatar"`
}

func toEntryUser(u *models.User) entryUser {
	return entryUser{ID: u.ID, Username: u.Username, Nickname: u.Nickname, DisplayName: u.PublicName(), Avatar: u.Avatar}
}

// Show GET /leaderboards/:key?period=all|month|week&page=&page_size=
// 榜单分页（得分倒序、ID 升序）；用户榜在登录时附带本人得分与名次 me。
func (b *behavior) Show(c *gin.Context) {
	core := b.core
	l, found := b.board(c.Param("key"))
	if !found {
		core.Fail(c, http.StatusNotFound, "排行榜不存在或未开放")
		return
	}
	meta := metaOf(l)
	period := c.DefaultQuery("period", plugincore.LeaderboardAll)
	if !slices.Contains(meta.Periods, period) {
		period = meta.Periods[0]
	}
	since := plugincore.LeaderboardSince(period, time.Now())
	page, pageSize := core.Paginate(c)
	pageSize = min(pageSize, maxPageSize)

	var total int64
	b.ranked(l, since).Count(&total)
	var rows []scoreRow
	b.ranked(l, since).Select("s.subject_id, s.value").Order("s.value DESC, s.subject_id ASC").
		Limit(pageSize).Offset((page - 1) * pageSize).Scan(&rows)
	ids := make([]uint, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.SubjectID)
	}
	var extras map[uint]any
	if l.Extras != nil && len(ids) > 0 {
		extras = l.Extras(core, ids)
	}
	items := make([]gin.H, 0, len(rows))
	if l.Kind == plugincore.LeaderboardBooks {
		books := map[uint]models.Book{}
		if len(ids) > 0 {
			var list []models.Book
			core.PreloadBookUser().Where("id IN ?", ids).Find(&list)
			core.AttachChapterCounts(list)
			core.DecorateBookList(list)
			for _, bk := range list {
				books[bk.ID] = bk
			}
		}
		for i, r := range rows {
			if bk, ok := books[r.SubjectID]; ok {
				items = append(items, entry(page, pageSize, i, r.Value, gin.H{"book": bk}, extras[r.SubjectID]))
			}
		}
	} else {
		users := map[uint]*models.User{}
		if len(ids) > 0 {
			var list []models.User
			core.Gorm().Where("id IN ?", ids).Find(&list)
			for i := range list {
				users[list[i].ID] = &list[i]
			}
		}
		for i, r := range rows {
			if u, ok := users[r.SubjectID]; ok {
				items = append(items, entry(page, pageSize, i, r.Value, gin.H{"user": toEntryUser(u)}, extras[r.SubjectID]))
			}
		}
	}
	out := gin.H{"board": meta, "items": items, "total": total, "page": page, "page_size": pageSize, "period": period}
	if u := core.CurrentUser(c); u != nil && l.Kind == plugincore.LeaderboardUsers {
		out["me"] = b.me(l, u, since)
	}
	core.OK(c, out)
}

func entry(page, pageSize, i int, value int64, subject gin.H, extra any) gin.H {
	subject["rank"] = (page-1)*pageSize + i + 1
	subject["value"] = value
	if extra != nil {
		subject["extra"] = extra
	}
	return subject
}

// me 本人在该榜的得分与名次：名次 = 公开榜中得分更高的人数 + 1；得分为 0（或不参与排名）时无名次。
func (b *behavior) me(l plugincore.Leaderboard, u *models.User, since *time.Time) gin.H {
	var value int64
	ranked := true
	var extra map[string]any
	if l.MyScore != nil {
		value, ranked, extra = l.MyScore(b.core, u.ID, since)
	} else {
		var row scoreRow
		b.core.Gorm().Table("(?) AS s", l.Scores(b.core, since)).Where("s.subject_id = ?", u.ID).Select("s.subject_id, s.value").Scan(&row)
		value = row.Value
	}
	me := gin.H{"value": value, "user": toEntryUser(u)}
	for k, v := range extra {
		me[k] = v
	}
	if ranked && value > 0 {
		var ahead int64
		b.ranked(l, since).Where("s.value > ? AND s.subject_id <> ?", value, u.ID).Count(&ahead)
		me["rank"] = ahead + 1
	}
	return me
}

type adminBoard struct {
	boardMeta
	Plugin        string `json:"plugin"`
	PluginEnabled bool   `json:"plugin_enabled"`
	Enabled       bool   `json:"enabled"`
}

func (b *behavior) adminItems() []adminBoard {
	states := b.states()
	out := make([]adminBoard, 0, len(states))
	for _, s := range states {
		out = append(out, adminBoard{boardMeta: metaOf(s.def), Plugin: s.def.Plugin, PluginEnabled: s.pluginEnabled, Enabled: s.enabled})
	}
	return out
}

// AdminList GET /admin/leaderboards 全部已登记的榜单：顺序、管理员开关与提供方插件是否可用。
func (b *behavior) AdminList(c *gin.Context) { b.core.OK(c, gin.H{"items": b.adminItems()}) }

// AdminUpdate PUT /admin/leaderboards {boards:[{key,enabled}]} 按给定顺序保存榜单顺序与开关。
func (b *behavior) AdminUpdate(c *gin.Context) {
	var req struct {
		Boards []struct {
			Key     string `json:"key"`
			Enabled bool   `json:"enabled"`
		} `json:"boards"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	known := map[string]bool{}
	for _, l := range plugincore.Leaderboards() {
		known[l.Key] = true
	}
	cfg := boardConfig{Order: []string{}, Disabled: []string{}}
	seen := map[string]bool{}
	for _, it := range req.Boards {
		key := strings.TrimSpace(it.Key)
		if !known[key] || seen[key] {
			b.core.Fail(c, http.StatusBadRequest, "未知或重复的榜单："+key)
			return
		}
		seen[key] = true
		cfg.Order = append(cfg.Order, key)
		if !it.Enabled {
			cfg.Disabled = append(cfg.Disabled, key)
		}
	}
	// 未提交的榜单保持原开关
	for _, s := range b.states() {
		if !seen[s.def.Key] && !s.enabled {
			cfg.Disabled = append(cfg.Disabled, s.def.Key)
		}
	}
	raw, _ := json.Marshal(cfg)
	if err := b.core.SetSetting(cfgBoards, string(raw), "排行榜：榜单顺序与开关"); err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	b.core.RecordAudit(c, "leaderboards.updated", "leaderboards", "settings", "排行榜设置", map[string]any{"order": cfg.Order, "disabled": cfg.Disabled})
	b.core.OK(c, gin.H{"items": b.adminItems()})
}
