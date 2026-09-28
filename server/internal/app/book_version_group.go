package app

import (
	"strconv"
	"strings"

	"gorm.io/gorm"

	"knowforge/server/internal/models"
)

// groupBookVersions 「版本聚合」：在 query 的筛选范围内，同一 version_group 只保留一本代表书
// （优先标记为「最新版」的，其次 id 最大即最新创建的），返回收窄后的查询与各版本组在该范围内的可见版本数
// （按版本号去重：译本继承原书的版本组，同一版本的多个语言只算一个版本；未填版本号的书各算一个）。
// 聚合在数据库侧完成，分页总数与每页条数都基于聚合后的结果，翻页不会出现空页/重复。
func (a *App) groupBookVersions(query *gorm.DB) (*gorm.DB, map[string]int) {
	type versionRow struct {
		ID              uint
		VersionGroup    string
		Version         string
		VersionIsLatest bool
	}
	var rows []versionRow
	counts := map[string]int{}
	if err := query.Session(&gorm.Session{}).
		Select("books.id, books.version_group, books.version, books.version_is_latest").
		Where("books.version_group <> ''").
		Find(&rows).Error; err != nil {
		return query, counts
	}
	reps := map[string]versionRow{}
	seen := map[uint]bool{}
	versions := map[string]bool{}
	for _, r := range rows {
		if seen[r.ID] { // 联表（标签/协作）可能产生重复行
			continue
		}
		seen[r.ID] = true
		v := strings.TrimSpace(r.Version)
		if v == "" {
			v = "id:" + strconv.FormatUint(uint64(r.ID), 10)
		}
		if !versions[r.VersionGroup+"\x00"+v] {
			versions[r.VersionGroup+"\x00"+v] = true
			counts[r.VersionGroup]++
		}
		cur, exists := reps[r.VersionGroup]
		if !exists || (r.VersionIsLatest && !cur.VersionIsLatest) || (r.VersionIsLatest == cur.VersionIsLatest && r.ID > cur.ID) {
			reps[r.VersionGroup] = r
		}
	}
	if len(reps) == 0 {
		return query, counts
	}
	ids := make([]uint, 0, len(reps))
	for _, r := range reps {
		ids = append(ids, r.ID)
	}
	return query.Where("(COALESCE(books.version_group, '') = '' OR books.id IN ?)", ids), counts
}

// attachVersionInfo 为列表书籍回填版本组信息（版本插件启用时）：
//   - counts 非空（聚合模式）时回填 VersionCount（>1 才有意义）；
//   - LatestVersion：组内被标记为「最新版」、且对当前用户可见的书籍的版本号。
func (a *App) attachVersionInfo(books []models.Book, counts map[string]int, u *models.User) {
	if len(books) == 0 || !a.pluginEnabled(pluginBookVersions) {
		return
	}
	groups := make([]string, 0, len(books))
	seen := map[string]bool{}
	for i := range books {
		g := books[i].VersionGroup
		if g == "" {
			continue
		}
		if n := counts[g]; n > 1 {
			books[i].VersionCount = n
		}
		if !seen[g] {
			seen[g] = true
			groups = append(groups, g)
		}
	}
	if len(groups) == 0 {
		return
	}
	var uid uint
	if u != nil {
		uid = u.ID
	}
	var latest []struct {
		VersionGroup string
		Version      string
	}
	a.DB.Model(&models.Book{}).Select("version_group, version").
		Where("version_group IN ? AND version_is_latest = ? AND version <> ''", groups, true).
		Where("(is_public = ? AND status IN ?) OR user_id = ?", true, publiclyReadableBookStatuses, uid).
		Order("id DESC").Find(&latest)
	latestByGroup := map[string]string{}
	for _, l := range latest {
		if _, ok := latestByGroup[l.VersionGroup]; !ok {
			latestByGroup[l.VersionGroup] = l.Version
		}
	}
	for i := range books {
		if v := latestByGroup[books[i].VersionGroup]; v != "" {
			books[i].LatestVersion = v
		}
	}
}
