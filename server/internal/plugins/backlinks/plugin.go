// Package backlinks 反向链接插件：记录章节之间的双向链接（[[章节]] 与 [文字](doc:章节) 两种写法），
// 阅读页底部显示「被引用」——哪些已发布章节链接到了本章；书籍设置里查看本书的章节链接关系与失效链接。
//
// 链接按「目标书籍 + 目标文字（章节 slug 或标题，小写）」保存，查询时再与章节当前的 slug/标题匹配，
// 因此目标章节改名、后来才创建的章节都不需要重算；只有来源章节的正文变化时才重建它的链接。
package backlinks

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

const (
	pluginKey  = plugins.KeyBacklinks
	cfgEnabled = "backlinks_enabled"
	maxTarget  = 255
)

// Link 一条章节链接：来源章节 → 目标书籍中 slug 或标题（小写）为 Target 的章节。
type Link struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	BookID   uint   `gorm:"index;not null" json:"book_id"` // 来源书籍
	DocID    uint   `gorm:"index;not null" json:"doc_id"`  // 来源章节
	ToBookID uint   `gorm:"not null;index:idx_doc_links_target,priority:1" json:"to_book_id"`
	Target   string `gorm:"size:255;not null;index:idx_doc_links_target,priority:2" json:"target"`
}

func (Link) TableName() string { return "doc_links" }

func init() {
	plugins.Register(plugins.Meta{
		Order:       131,
		Key:         pluginKey,
		Name:        "反向链接",
		Description: "章节可用 [[章节标题]] 或 [文字](doc:章节) 互相链接；阅读页底部显示「被引用」，列出链接到本章的已发布章节；书籍设置中可查看本书的章节链接关系与失效链接。默认关闭。",
		Kind:        plugins.KindFeature,
		Builtin:     true,
		EnabledKey:  cfgEnabled,
		Models:      []any{&Link{}},
		Tables:      []string{"doc_links"},
	})
	plugincore.RegisterBehavior(&behavior{})
	plugincore.RegisterBookDataModels(&Link{})
	onChange := func(core plugincore.Core, book *models.Book, doc *models.Document) {
		if core.PluginEnabled(pluginKey) {
			(&behavior{core: core}).indexDoc(book, doc)
		}
	}
	plugincore.OnChapterPublished(onChange)
	plugincore.OnChapterContentChanged(onChange)
	plugincore.OnBookCopied(func(core plugincore.Core, _, dst *models.Book) error {
		if core.PluginEnabled(pluginKey) {
			(&behavior{core: core}).indexBook(dst)
		}
		return nil
	})
	// 启用时为全部已有章节建立索引（只处理正文里含链接写法的章节）
	plugincore.OnPluginEnabled(pluginKey, func(core plugincore.Core) error {
		return (&behavior{core: core}).indexAll()
	})
}

type behavior struct{ core plugincore.Core }

func (b *behavior) Key() string { return pluginKey }

// —— 解析 ——

var (
	fencedCode = regexp.MustCompile("(?ms)^[ \t]*(```|~~~).*?^[ \t]*(```|~~~)[ \t]*$")
	inlineCode = regexp.MustCompile("`[^`\n]*`")
	wikiLink   = regexp.MustCompile(`\[\[([^\[\]\n|]+)(?:\|([^\[\]\n]+))?\]\]`)
	docLink    = regexp.MustCompile(`\[([^\[\]\n]*)\]\(doc:([^)\s]+)\)`)
)

// ref 正文中的一处链接：Book 为空表示同书；Target 为章节 slug 或标题（原样）。
type ref struct {
	Book   string
	Target string
	Label  string
	Start  int
	End    int
}

// blankCode 把代码块与行内代码替换为等长空白（保持其余内容的位置不变），代码中的链接写法不算链接。
func blankCode(s string) string {
	blank := func(m string) string { return strings.Repeat(" ", len(m)) }
	s = fencedCode.ReplaceAllStringFunc(s, blank)
	return inlineCode.ReplaceAllStringFunc(s, blank)
}

// parseRefs 提取正文中的全部链接（按出现顺序）。
func parseRefs(content string) []ref {
	if !strings.Contains(content, "[[") && !strings.Contains(content, "](doc:") {
		return nil
	}
	s := blankCode(content)
	out := []ref{}
	for _, m := range wikiLink.FindAllStringSubmatchIndex(s, -1) {
		r := ref{Target: strings.TrimSpace(s[m[2]:m[3]]), Start: m[0], End: m[1]}
		if m[4] >= 0 {
			r.Label = strings.TrimSpace(s[m[4]:m[5]])
		}
		if i := strings.Index(r.Target, "/"); i >= 0 {
			r.Book, r.Target = strings.TrimSpace(r.Target[:i]), strings.TrimSpace(r.Target[i+1:])
			if r.Book == "" {
				continue
			}
		}
		if r.Target != "" {
			out = append(out, r)
		}
	}
	for _, m := range docLink.FindAllStringSubmatchIndex(s, -1) {
		r := ref{Label: s[m[2]:m[3]], Target: strings.TrimLeft(s[m[4]:m[5]], "/"), Start: m[0], End: m[1]}
		if i := strings.Index(r.Target, "/"); i >= 0 {
			r.Book, r.Target = r.Target[:i], r.Target[i+1:]
		}
		if r.Target != "" {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

// norm 目标的比较形式：去首尾空白、小写。
func norm(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// —— 索引 ——

// indexDoc 重建一个章节的出链。
func (b *behavior) indexDoc(book *models.Book, doc *models.Document) {
	db := b.core.Gorm()
	books := map[string]uint{}
	rows := []Link{}
	seen := map[string]bool{}
	for _, r := range parseRefs(doc.Content) {
		to := book.ID
		if r.Book != "" && r.Book != book.Slug {
			id, ok := books[r.Book]
			if !ok {
				var target models.Book
				if db.Select("id").Where("slug = ?", r.Book).First(&target).Error == nil {
					id = target.ID
				}
				books[r.Book] = id
			}
			if id == 0 {
				continue
			}
			to = id
		}
		t := norm(r.Target)
		if utf8.RuneCountInString(t) > maxTarget || (to == book.ID && (t == norm(doc.Slug) || t == norm(doc.Title))) {
			continue
		}
		key := strconv.FormatUint(uint64(to), 10) + "\x00" + t
		if seen[key] {
			continue
		}
		seen[key] = true
		rows = append(rows, Link{BookID: book.ID, DocID: doc.ID, ToBookID: to, Target: t})
	}
	db.Where("doc_id = ?", doc.ID).Delete(&Link{})
	if len(rows) > 0 {
		db.CreateInBatches(rows, 200)
	}
}

// indexBook 重建一本书全部章节的出链。
func (b *behavior) indexBook(book *models.Book) {
	db := b.core.Gorm()
	db.Where("book_id = ?", book.ID).Delete(&Link{})
	var docs []models.Document // 只取正文含链接写法的章节，避免加载全部正文
	db.Where("book_id = ? AND (content LIKE ? OR content LIKE ?)", book.ID, "%[[%", "%](doc:%").Find(&docs)
	for i := range docs {
		b.indexDoc(book, &docs[i])
	}
}

// indexAll 重建全站索引。
func (b *behavior) indexAll() error {
	db := b.core.Gorm()
	if err := db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&Link{}).Error; err != nil {
		return err
	}
	var bookIDs []uint
	if err := db.Model(&models.Document{}).Where("content LIKE ? OR content LIKE ?", "%[[%", "%](doc:%").Distinct().Pluck("book_id", &bookIDs).Error; err != nil {
		return err
	}
	for _, id := range bookIDs {
		var book models.Book
		if db.First(&book, id).Error == nil {
			b.indexBook(&book)
		}
	}
	return nil
}
