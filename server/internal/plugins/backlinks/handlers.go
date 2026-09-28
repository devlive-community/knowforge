package backlinks

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

const (
	maxBacklinks = 50
	beforeRunes  = 60
	afterRunes   = 80
)

func (b *behavior) RegisterRoutes(api *gin.RouterGroup, core plugincore.Core) {
	b.core = core
	feat := core.RequireFeaturePlugin(pluginKey)
	api.GET("/backlinks/docs/:id", core.OptionalAuth(), feat, b.Backlinks)
	api.GET("/backlinks/books/:id/graph", core.RequireAuth(), feat, b.Graph)
}

// —— 被引用 ——

// snippet 来源章节中链接所在段落的上下文（Text 为链接文字，前后为纯文本）。
type snippet struct {
	Before string `json:"before"`
	Text   string `json:"text"`
	After  string `json:"after"`
}

type backlink struct {
	ID        uint     `json:"id"`
	Title     string   `json:"title"`
	Slug      string   `json:"slug"`
	BookID    uint     `json:"book_id"`
	BookSlug  string   `json:"book_slug"`
	BookTitle string   `json:"book_title"`
	Excerpt   *snippet `json:"excerpt"`
}

var (
	plainImage   = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	plainLink    = regexp.MustCompile(`\[([^\[\]]*)\]\([^)]*\)`)
	plainHTML    = regexp.MustCompile(`<[^>]+>`)
	plainMarkers = regexp.MustCompile(`(?m)^\s{0,3}(#{1,6}|>|[-+*]|\d+\.)\s+`)
	plainInline  = regexp.MustCompile("[*_~`]")
	plainSpaces  = regexp.MustCompile(`\s+`)
)

// plain Markdown 片段转为纯文本（双向链接取显示文字）。
func plain(s string) string {
	s = wikiLink.ReplaceAllStringFunc(s, func(m string) string {
		sub := wikiLink.FindStringSubmatch(m)
		if sub[2] != "" {
			return sub[2]
		}
		t := sub[1]
		if i := strings.Index(t, "/"); i >= 0 {
			t = t[i+1:]
		}
		return t
	})
	s = plainImage.ReplaceAllString(s, " ")
	s = plainLink.ReplaceAllString(s, "$1")
	s = plainHTML.ReplaceAllString(s, " ")
	s = plainMarkers.ReplaceAllString(s, "")
	s = plainInline.ReplaceAllString(s, "")
	return plainSpaces.ReplaceAllString(s, " ")
}

// excerptAround 链接所在段落，前后各截取一段。
func excerptAround(content string, r ref, text string) *snippet {
	start := strings.LastIndex(content[:r.Start], "\n\n")
	if start < 0 {
		start = 0
	} else {
		start += 2
	}
	end := strings.Index(content[r.End:], "\n\n")
	if end < 0 {
		end = len(content)
	} else {
		end += r.End
	}
	before := []rune(strings.TrimLeft(plain(content[start:r.Start]), " "))
	after := []rune(strings.TrimRight(plain(content[r.End:end]), " "))
	sn := &snippet{Before: string(before), Text: text, After: string(after)}
	if len(before) > beforeRunes {
		sn.Before = "…" + string(before[len(before)-beforeRunes:])
	}
	if len(after) > afterRunes {
		sn.After = string(after[:afterRunes]) + "…"
	}
	return sn
}

// Backlinks GET /backlinks/docs/:id 链接到本章的已发布章节（读者可读的书籍）；付费章节不给出上下文。
func (b *behavior) Backlinks(c *gin.Context) {
	db := b.core.Gorm()
	u := b.core.CurrentUser(c)
	var doc models.Document
	var book models.Book
	if db.First(&doc, c.Param("id")).Error != nil || db.First(&book, doc.BookID).Error != nil || !b.core.CanReadBook(u, &book) {
		b.core.Fail(c, http.StatusNotFound, "章节不存在")
		return
	}
	targets := []string{norm(doc.Slug)}
	if t := norm(doc.Title); t != "" && t != targets[0] {
		// [[标题]] 与本书另一章节的 slug 相同时指向那一章（slug 优先，与渲染一致）
		var n int64
		db.Model(&models.Document{}).Where("book_id = ? AND id <> ? AND LOWER(slug) = ?", book.ID, doc.ID, t).Count(&n)
		if n == 0 {
			targets = append(targets, t)
		}
	}
	var ids []uint
	db.Model(&Link{}).Where("to_book_id = ? AND target IN ? AND doc_id <> ?", book.ID, targets, doc.ID).Distinct().Pluck("doc_id", &ids)
	items := []backlink{}
	if len(ids) == 0 {
		b.core.OK(c, gin.H{"items": items})
		return
	}
	var sources []models.Document
	db.Where("id IN ? AND status = ?", ids, "published").Order("book_id ASC, sort_order ASC, id ASC").Find(&sources)
	books := map[uint]*models.Book{book.ID: &book}
	readable := map[uint]bool{book.ID: true}
	for i := range sources {
		src := &sources[i]
		sb, ok := books[src.BookID]
		if !ok {
			var loaded models.Book
			if db.First(&loaded, src.BookID).Error == nil {
				sb = &loaded
				readable[src.BookID] = b.core.CanReadBook(u, sb)
			}
			books[src.BookID] = sb
		}
		if sb == nil || !readable[src.BookID] {
			continue
		}
		item := backlink{ID: src.ID, Title: src.Title, Slug: src.Slug, BookID: sb.ID, BookSlug: sb.Slug, BookTitle: sb.Title}
		if plugincore.CheckContentAccess(b.core, u, sb, src).Allowed {
			for _, r := range parseRefs(src.Content) {
				sameBook := (r.Book == "" && src.BookID == book.ID) || r.Book == book.Slug
				if sameBook && contains(targets, norm(r.Target)) {
					text := r.Label
					if text == "" {
						text = doc.Title
					}
					item.Excerpt = excerptAround(src.Content, r, text)
					break
				}
			}
		}
		items = append(items, item)
		if len(items) >= maxBacklinks {
			break
		}
	}
	b.core.OK(c, gin.H{"items": items})
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// —— 章节链接关系（作者） ——

type graphNode struct {
	ID       uint   `json:"id"`
	ParentID *uint  `json:"parent_id"`
	Title    string `json:"title"`
	Slug     string `json:"slug"`
	Status   string `json:"status"`
	Outgoing []uint `json:"outgoing"`
	Incoming []uint `json:"incoming"`
	External int    `json:"external"` // 指向其他书籍的有效链接数
}

type brokenLink struct {
	From   uint   `json:"from"`
	Target string `json:"target"` // 原样的目标（跨书为 书籍slug/目标）
}

// Graph GET /backlinks/books/:id/graph 本书章节之间的链接（含草稿），以及无法解析的失效链接；顺带重建本书索引。
func (b *behavior) Graph(c *gin.Context) {
	book, status := b.core.FindBook(c)
	if book == nil {
		b.core.Fail(c, status, "书籍不存在")
		return
	}
	if !b.core.CanEditBookContent(b.core.CurrentUser(c), book) {
		b.core.Fail(c, http.StatusForbidden, "没有编辑这本书的权限")
		return
	}
	db := b.core.Gorm()
	var docs []models.Document
	db.Where("book_id = ?", book.ID).Order("sort_order ASC, id ASC").Find(&docs)
	bySlug, byTitle := map[string]uint{}, map[string]uint{}
	for _, d := range docs {
		bySlug[norm(d.Slug)] = d.ID
		if _, ok := byTitle[norm(d.Title)]; !ok {
			byTitle[norm(d.Title)] = d.ID
		}
	}
	resolve := func(slugs, titles map[string]uint, target string) uint {
		if id, ok := slugs[norm(target)]; ok {
			return id
		}
		return titles[norm(target)]
	}
	// 跨书目标：按书籍 slug 加载其章节索引（同一书只查一次）
	type index struct{ slugs, titles map[string]uint }
	others := map[string]*index{}
	otherIndex := func(slug string) *index {
		if ix, ok := others[slug]; ok {
			return ix
		}
		var ob models.Book
		var ix *index
		if db.Where("slug = ?", slug).First(&ob).Error == nil {
			ix = &index{slugs: map[string]uint{}, titles: map[string]uint{}}
			var rows []models.Document
			db.Select("id", "slug", "title").Where("book_id = ?", ob.ID).Order("sort_order ASC, id ASC").Find(&rows)
			for _, d := range rows {
				ix.slugs[norm(d.Slug)] = d.ID
				if _, ok := ix.titles[norm(d.Title)]; !ok {
					ix.titles[norm(d.Title)] = d.ID
				}
			}
		}
		others[slug] = ix
		return ix
	}

	nodes := make([]graphNode, len(docs))
	pos := map[uint]int{}
	for i, d := range docs {
		nodes[i] = graphNode{ID: d.ID, ParentID: d.ParentID, Title: d.Title, Slug: d.Slug, Status: d.Status, Outgoing: []uint{}, Incoming: []uint{}}
		pos[d.ID] = i
	}
	broken := []brokenLink{}
	edges := 0
	for i, d := range docs {
		seen := map[uint]bool{}
		for _, r := range parseRefs(d.Content) {
			if r.Book != "" && r.Book != book.Slug {
				ix := otherIndex(r.Book)
				if ix == nil || resolve(ix.slugs, ix.titles, r.Target) == 0 {
					broken = append(broken, brokenLink{From: d.ID, Target: r.Book + "/" + r.Target})
				} else {
					nodes[i].External++
				}
				continue
			}
			to := resolve(bySlug, byTitle, r.Target)
			if to == 0 {
				broken = append(broken, brokenLink{From: d.ID, Target: r.Target})
				continue
			}
			if to == d.ID || seen[to] {
				continue
			}
			seen[to] = true
			nodes[i].Outgoing = append(nodes[i].Outgoing, to)
			nodes[pos[to]].Incoming = append(nodes[pos[to]].Incoming, d.ID)
			edges++
		}
	}
	b.indexBook(book)
	b.core.OK(c, gin.H{"nodes": nodes, "edges": edges, "broken": broken})
}
