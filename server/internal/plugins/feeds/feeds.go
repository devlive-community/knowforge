// Package feeds RSS 订阅插件：每本公开书籍与每位作者各提供一个 RSS 2.0 订阅地址，列出最近发布的章节。
// 付费章节只给出与付费墙相同的试读摘要，并注明为付费章节；只包含游客可读的公开书籍。
// 章节首次发布的时间由本插件记录（订阅章节发布事件）；启用前已发布的章节以创建时间为准。
package feeds

import (
	"encoding/xml"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

const (
	pluginKey    = plugins.KeyFeeds
	cfgEnabled   = "feeds_enabled"
	itemsPerFeed = 20
	excerptRunes = 300
	cacheControl = "public, max-age=600"
)

// Entry 章节首次发布的时间。
type Entry struct {
	DocID       uint      `gorm:"primaryKey" json:"doc_id"`
	BookID      uint      `gorm:"index" json:"book_id"`
	PublishedAt time.Time `gorm:"index" json:"published_at"`
}

func (Entry) TableName() string { return "feed_entries" }

func init() {
	plugins.Register(plugins.Meta{
		Order:       129,
		Key:         pluginKey,
		Name:        "RSS 订阅",
		Description: "为每本公开书籍与每位作者提供 RSS 订阅地址，列出最近发布的章节；付费章节只给出试读摘要。书籍详情页与作者主页显示订阅入口，并支持阅读器自动发现。默认关闭。",
		Kind:        plugins.KindFeature,
		Builtin:     true,
		EnabledKey:  cfgEnabled,
		Models:      []any{&Entry{}},
		Tables:      []string{"feed_entries"},
	})
	plugincore.RegisterBehavior(&behavior{})
	plugincore.OnChapterPublished(func(core plugincore.Core, book *models.Book, doc *models.Document) {
		if core.PluginEnabled(pluginKey) {
			core.Gorm().Clauses(clause.OnConflict{DoNothing: true}).Create(&Entry{DocID: doc.ID, BookID: book.ID, PublishedAt: time.Now()})
		}
	})
}

type behavior struct{ core plugincore.Core }

func (b *behavior) Key() string { return pluginKey }

func (b *behavior) RegisterRoutes(api *gin.RouterGroup, core plugincore.Core) {
	b.core = core
	feat := core.RequireFeaturePlugin(pluginKey)
	api.GET("/feeds/books/:file", feat, b.BookFeed)
	api.GET("/feeds/users/:file", feat, b.UserFeed)
}

// —— RSS 2.0 ——

type rss struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	Atom    string   `xml:"xmlns:atom,attr"`
	Channel channel  `xml:"channel"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

type channel struct {
	Title         string   `xml:"title"`
	Link          string   `xml:"link"`
	Description   string   `xml:"description"`
	Language      string   `xml:"language,omitempty"`
	LastBuildDate string   `xml:"lastBuildDate"`
	Generator     string   `xml:"generator"`
	Self          atomLink `xml:"atom:link"`
	Items         []item   `xml:"item"`
}

type guid struct {
	Value       string `xml:",chardata"`
	IsPermaLink bool   `xml:"isPermaLink,attr"`
}

type item struct {
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	GUID        guid     `xml:"guid"`
	PubDate     string   `xml:"pubDate"`
	Description string   `xml:"description"`
	Category    []string `xml:"category,omitempty"`
}

// baseURL 站点访问地址（未设置时按请求推断）。
func (b *behavior) baseURL(c *gin.Context) string {
	if u := strings.TrimRight(b.core.GetSetting("site_url"), "/"); u != "" {
		return u
	}
	scheme := "http"
	if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + c.Request.Host
}

func (b *behavior) siteName() string {
	if n := b.core.GetSetting("site_name"); n != "" {
		return n
	}
	return "KnowForge"
}

var (
	mdCode    = regexp.MustCompile("(?s)```.*?```")
	mdImage   = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	mdLink    = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdHTML    = regexp.MustCompile(`<[^>]+>`)
	mdMarkers = regexp.MustCompile(`(?m)^\s{0,3}(#{1,6}|>|[-+*]|\d+\.)\s+`)
	mdInline  = regexp.MustCompile("[*_~`]")
	spaces    = regexp.MustCompile(`\s+`)
)

// excerpt Markdown 转为纯文本摘要。
func excerpt(md string, n int) string {
	s := mdCode.ReplaceAllString(md, " ")
	s = mdImage.ReplaceAllString(s, " ")
	s = mdLink.ReplaceAllString(s, "$1")
	s = mdHTML.ReplaceAllString(s, " ")
	s = mdMarkers.ReplaceAllString(s, "")
	s = mdInline.ReplaceAllString(s, "")
	s = strings.TrimSpace(spaces.ReplaceAllString(s, " "))
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// publishedDoc 章节与首次发布时间。
type publishedDoc struct {
	models.Document
	FirstPublished time.Time
}

// recentDocs 公开书籍中最近发布的章节（bookIDs 限定范围）：有发布记录的按记录时间，其余按创建时间，合并后取最新的若干条。
func (b *behavior) recentDocs(bookIDs []uint) []publishedDoc {
	out := []publishedDoc{}
	if len(bookIDs) == 0 {
		return out
	}
	db := b.core.Gorm()
	var entries []Entry
	db.Where("book_id IN ?", bookIDs).Order("published_at DESC").Limit(itemsPerFeed).Find(&entries)
	at := map[uint]time.Time{}
	ids := []uint{}
	for _, e := range entries {
		at[e.DocID] = e.PublishedAt
		ids = append(ids, e.DocID)
	}
	if len(ids) > 0 {
		var docs []models.Document
		db.Where("id IN ? AND status = ?", ids, "published").Find(&docs)
		for _, d := range docs {
			out = append(out, publishedDoc{Document: d, FirstPublished: at[d.ID]})
		}
	}
	// 启用本插件之前发布的章节没有记录：按创建时间
	var recorded []uint
	db.Model(&Entry{}).Where("book_id IN ?", bookIDs).Pluck("doc_id", &recorded)
	q := db.Where("book_id IN ? AND status = ?", bookIDs, "published")
	if len(recorded) > 0 {
		q = q.Where("id NOT IN ?", recorded)
	}
	var older []models.Document
	q.Order("created_at DESC").Limit(itemsPerFeed).Find(&older)
	for _, d := range older {
		out = append(out, publishedDoc{Document: d, FirstPublished: d.CreatedAt})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].FirstPublished.Equal(out[j].FirstPublished) {
			return out[i].FirstPublished.After(out[j].FirstPublished)
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > itemsPerFeed {
		out = out[:itemsPerFeed]
	}
	return out
}

// toItem 章节条目：付费章节只给出试读摘要。
func (b *behavior) toItem(base string, book *models.Book, d publishedDoc, withBook bool) item {
	link := base + "/book/reader/" + book.Slug + "/" + d.Slug
	title := d.Title
	if withBook {
		title = book.Title + " · " + d.Title
	}
	desc := excerpt(d.Content, excerptRunes)
	if d.ExternalURL != "" {
		desc = "外部链接：" + d.ExternalURL
	}
	var categories []string
	if access := plugincore.CheckContentAccess(b.core, nil, book, &d.Document); !access.Allowed {
		desc = excerpt(access.Preview, excerptRunes) + "（付费章节，全文请在站内阅读）"
		categories = []string{"付费"}
	}
	return item{Title: title, Link: link, GUID: guid{Value: link, IsPermaLink: true}, PubDate: d.FirstPublished.UTC().Format(time.RFC1123Z),
		Description: desc, Category: categories}
}

func (b *behavior) write(c *gin.Context, feed rss) {
	out, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "生成订阅失败")
		return
	}
	c.Header("Cache-Control", cacheControl)
	c.Data(http.StatusOK, "application/rss+xml; charset=utf-8", append([]byte(xml.Header), out...))
}

func newFeed(base, self, title, link, desc, lang string, items []item) rss {
	build := time.Now().UTC().Format(time.RFC1123Z)
	if len(items) > 0 {
		build = items[0].PubDate
	}
	return rss{Version: "2.0", Atom: "http://www.w3.org/2005/Atom", Channel: channel{
		Title: title, Link: link, Description: desc, Language: lang, LastBuildDate: build, Generator: "KnowForge",
		Self: atomLink{Href: base + self, Rel: "self", Type: "application/rss+xml"}, Items: items,
	}}
}

// BookFeed GET /feeds/books/:slug.xml 一本公开书籍最近发布的章节。
func (b *behavior) BookFeed(c *gin.Context) {
	slug := strings.TrimSuffix(c.Param("file"), ".xml")
	var book models.Book
	if b.core.Gorm().Where("slug = ?", slug).First(&book).Error != nil || !b.core.CanReadBook(nil, &book) {
		b.core.Fail(c, http.StatusNotFound, "书籍不存在")
		return
	}
	base := b.baseURL(c)
	items := []item{}
	for _, d := range b.recentDocs([]uint{book.ID}) {
		items = append(items, b.toItem(base, &book, d, false))
	}
	desc := excerpt(book.Description, 500)
	if desc == "" {
		desc = book.Title
	}
	b.write(c, newFeed(base, "/api/v1/feeds/books/"+book.Slug+".xml", book.Title+" - "+b.siteName(), base+"/book/detail/"+book.Slug, desc, "", items))
}

// UserFeed GET /feeds/users/:username.xml 一位作者的公开书籍最近发布的章节。
func (b *behavior) UserFeed(c *gin.Context) {
	name := strings.TrimSuffix(c.Param("file"), ".xml")
	db := b.core.Gorm()
	var u models.User
	if db.Where("username = ? AND is_active = ?", name, true).First(&u).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "用户不存在")
		return
	}
	var candidates []models.Book
	db.Where("user_id = ?", u.ID).Find(&candidates)
	books := map[uint]*models.Book{}
	ids := []uint{}
	for i := range candidates {
		if b.core.CanReadBook(nil, &candidates[i]) {
			books[candidates[i].ID] = &candidates[i]
			ids = append(ids, candidates[i].ID)
		}
	}
	base := b.baseURL(c)
	items := []item{}
	for _, d := range b.recentDocs(ids) {
		items = append(items, b.toItem(base, books[d.BookID], d, true))
	}
	author := u.PublicName()
	b.write(c, newFeed(base, "/api/v1/feeds/users/"+u.Username+".xml", author+" - "+b.siteName(), base+"/user/"+u.Username, author+" 最近发布的章节", "", items))
}
