package bookversions

import (
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// bookVariant 分组内的一本书（版本组），供阅读页切换。
type bookVariant struct {
	Slug         string `json:"slug"`
	Title        string `json:"title"`
	Language     string `json:"language"`
	Version      string `json:"version"`
	Current      bool   `json:"current"`
	IsLatest     bool   `json:"is_latest"`
	FirstDocSlug string `json:"first_doc_slug"`
}

// sameLanguage 启用「书籍多语言」时只保留与当前书籍同语言的版本：译本只做了部分版本时，
// 切换版本不应列出其他语言才有的版本（否则误以为这些版本都有该语言）。未启用时不按语言筛选。
func sameLanguage(core plugincore.Core, books []models.Book, current *models.Book) []models.Book {
	if !core.PluginEnabled(plugins.KeyBookTranslations) {
		return books
	}
	out := make([]models.Book, 0, len(books))
	for _, b := range books {
		if strings.TrimSpace(b.Language) == strings.TrimSpace(current.Language) {
			out = append(out, b)
		}
	}
	return out
}

// dedupeVersions 版本组内每个版本号只保留一本：译本会继承原书的版本组，同一版本的多个语言不应重复出现。
// 优先当前书籍，其次与当前书籍同语言的，否则取最早创建的（通常是原书）；未填版本号的书各自保留。保持输入顺序。
func dedupeVersions(books []models.Book, current *models.Book) []models.Book {
	pick := map[string]int{}
	key := func(b *models.Book) string {
		if v := strings.TrimSpace(b.Version); v != "" {
			return "v:" + v
		}
		return "id:" + strconv.FormatUint(uint64(b.ID), 10)
	}
	rank := func(b *models.Book) int {
		switch {
		case b.ID == current.ID:
			return 0
		case b.Language == current.Language:
			return 1
		default:
			return 2
		}
	}
	for i := range books {
		k := key(&books[i])
		j, ok := pick[k]
		if !ok || rank(&books[i]) < rank(&books[j]) || (rank(&books[i]) == rank(&books[j]) && books[i].ID < books[j].ID) {
			pick[k] = i
		}
	}
	out := make([]models.Book, 0, len(pick))
	for i := range books {
		if pick[key(&books[i])] == i {
			out = append(out, books[i])
		}
	}
	return out
}

// bookGroupSiblings 返回版本组内对当前用户可见的书籍（含自身），每个版本号一本（见 dedupeVersions）。
func bookGroupSiblings(core plugincore.Core, u *models.User, book *models.Book, column, value string) []bookVariant {
	if strings.TrimSpace(value) == "" {
		return []bookVariant{}
	}
	var all []models.Book
	core.Gorm().Where(column+" = ?", value).Order("id ASC").Find(&all)
	readable := make([]models.Book, 0, len(all))
	for i := range all {
		if core.CanReadBook(u, &all[i]) {
			readable = append(readable, all[i])
		}
	}
	books := dedupeVersions(sameLanguage(core, readable, book), book)
	out := make([]bookVariant, 0, len(books))
	for i := range books {
		b := &books[i]
		var firstDocSlug string
		core.Gorm().Model(&models.Document{}).Where("book_id = ? AND status = ?", b.ID, "published").
			Order("sort_order ASC, created_at ASC").Limit(1).Pluck("slug", &firstDocSlug)
		out = append(out, bookVariant{Slug: b.Slug, Title: b.Title, Language: b.Language, Version: b.Version, Current: b.ID == book.ID, IsLatest: b.VersionIsLatest, FirstDocSlug: firstDocSlug})
	}
	if len(out) <= 1 {
		return []bookVariant{}
	}
	return out
}

type behavior struct{ core plugincore.Core }

func init() { plugincore.RegisterBehavior(&behavior{}) }

func (b *behavior) Key() string { return plugins.KeyBookVersions }

func (b *behavior) RegisterRoutes(api *gin.RouterGroup, core plugincore.Core) {
	b.core = core
	api.GET("/books/:id/versions", core.OptionalAuth(), core.RequireFeaturePlugin(plugins.KeyBookVersions), b.GetBookVersions)
	api.GET("/books/:id/versions/books", core.OptionalAuth(), core.RequireFeaturePlugin(plugins.KeyBookVersions), b.ListBookVersionBooks)
}

// GetBookVersions GET /books/:id/versions 同一版本组内、对当前用户可见的书籍（含自身），供阅读页版本切换。
func (b *behavior) GetBookVersions(c *gin.Context) {
	core := b.core
	book, status := core.FindBook(c)
	if book == nil {
		core.Fail(c, status, "书籍不存在")
		return
	}
	u := core.CurrentUser(c)
	if !core.CanReadBook(u, book) {
		core.Fail(c, http.StatusNotFound, "书籍不存在")
		return
	}
	core.OK(c, gin.H{"items": bookGroupSiblings(core, u, book, "version_group", book.VersionGroup)})
}

var versionNumberRe = regexp.MustCompile(`\d+`)

// compareVersions 按版本号中的数字逐段比较（v1.10 > v1.9），数字相同再按字符串比较；与前端 cmpVersion 一致。
func compareVersions(a, b string) int {
	pa, pb := versionNumberRe.FindAllString(a, -1), versionNumberRe.FindAllString(b, -1)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var na, nb int
		if i < len(pa) {
			na, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			nb, _ = strconv.Atoi(pb[i])
		}
		if na != nb {
			if na < nb {
				return -1
			}
			return 1
		}
	}
	return strings.Compare(a, b)
}

// ListBookVersionBooks GET /books/:id/versions/books?page=&page_size=
// 版本组内对当前用户可见的全部书籍（含自身，完整书籍卡片数据），按站点「版本排序」配置排序后分页；供列表「N 个版本」弹框使用。
func (b *behavior) ListBookVersionBooks(c *gin.Context) {
	core := b.core
	book, status := core.FindBook(c)
	if book == nil {
		core.Fail(c, status, "书籍不存在")
		return
	}
	u := core.CurrentUser(c)
	if !core.CanReadBook(u, book) {
		core.Fail(c, http.StatusNotFound, "书籍不存在")
		return
	}
	page, pageSize := core.Paginate(c)
	books := []models.Book{}
	if strings.TrimSpace(book.VersionGroup) == "" {
		books = append(books, *book)
	} else {
		var all []models.Book
		if err := core.PreloadBookUser().Where("version_group = ?", book.VersionGroup).Find(&all).Error; err != nil {
			core.Fail(c, http.StatusInternalServerError, "查询失败")
			return
		}
		for i := range all {
			if core.CanReadBook(u, &all[i]) {
				books = append(books, all[i])
			}
		}
		books = dedupeVersions(sameLanguage(core, books, book), book)
	}
	asc := core.GetSetting("book_versions_sort") == "asc"
	sort.SliceStable(books, func(i, j int) bool {
		d := compareVersions(books[i].Version, books[j].Version)
		if d == 0 {
			return books[i].ID > books[j].ID
		}
		if asc {
			return d < 0
		}
		return d > 0
	})
	total := len(books)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	items := books[start:end]
	core.AttachChapterCounts(items)
	core.DecorateBookList(items)
	core.OK(c, plugincore.PageResult{Items: items, Total: int64(total), Page: page, PageSize: pageSize})
}
