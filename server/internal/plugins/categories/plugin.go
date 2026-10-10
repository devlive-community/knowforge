// Package categories 书籍分类插件：管理员维护最多三层的分类树，作者可为书籍选择一个分类（可选，不选即未分类）。
// 发现页按分类浏览（/explore?category=<slug>，含子分类的书），书籍详情显示分类路径；分类页进入 Sitemap 并推送 IndexNow。
// 管理员可在后台按关键词、当前分类筛选书籍并批量归类。分类只做导航，与作者自由打的标签互不影响。
package categories

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"gorm.io/gorm"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

const pluginKey = plugins.KeyCategories

// PermManage 维护分类树、批量归类书籍（管理员）。
const PermManage authz.Permission = "categories:manage"

const (
	maxDepth      = 3   // 分类最多三层
	maxCategories = 500 // 分类总数上限（整棵树一次加载）
	maxName       = 40
	maxDesc       = 300
	maxSlug       = 60
	maxBatch      = 500 // 一次批量归类的书籍数上限
)

// Category 一个分类。ParentID 为 0 表示顶级分类。
type Category struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	ParentID    uint      `gorm:"index;not null;default:0" json:"parent_id"`
	Name        string    `gorm:"size:80;not null" json:"name"`
	Slug        string    `gorm:"size:80;uniqueIndex;not null" json:"slug"`
	Description string    `gorm:"size:600" json:"description"`
	IconType    string    `gorm:"size:10;default:''" json:"icon_type"` // '' | fa | image | svg
	IconValue   string    `gorm:"size:500;default:''" json:"icon_value"`
	SortOrder   int       `gorm:"default:0" json:"sort_order"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Assignment 书籍所属的分类（每本书至多一个）。
type Assignment struct {
	BookID     uint      `gorm:"primaryKey;autoIncrement:false" json:"book_id"`
	CategoryID uint      `gorm:"index;not null" json:"category_id"`
	CreatedAt  time.Time `json:"created_at"`
}

func (Category) TableName() string   { return "book_categories" }
func (Assignment) TableName() string { return "book_category_assignments" }

type behavior struct{ core plugincore.Core }

func (b *behavior) Key() string { return pluginKey }

func init() {
	plugincore.RegisterURLColumns("book_categories", "icon_value") // 存储迁移：分类图标
	plugins.Register(plugins.Meta{
		Order:       85,
		Key:         pluginKey,
		Name:        "书籍分类",
		Description: "管理员维护最多三层的书籍分类，作者可为书籍选择一个分类（可选）；发现页按分类浏览、书籍详情显示分类路径，分类页进入 Sitemap。后台可批量归类书籍。与标签互不影响。默认关闭，禁用后分类页面与接口停用，数据保留。",
		Kind:        plugins.KindFeature,
		Builtin:     true,
		Models:      []any{&Category{}, &Assignment{}},
		Tables:      []string{"book_category_assignments", "book_categories"},
		AdminPerms:  []authz.Permission{PermManage},
	})
	plugincore.RegisterBehavior(&behavior{})
	plugincore.RegisterBookDataModels(&Assignment{})

	// 列表 / 详情回填书籍分类（含路径）
	plugincore.OnDecorateBooks(func(core plugincore.Core, books []*models.Book) {
		(&behavior{core: core}).attach(books)
	})
	// 创建 / 更新书籍时的 category_id：正整数为分类 ID，0 为未分类
	plugincore.RegisterBookField(plugincore.BookField{
		Name: "category_id",
		Validate: func(raw json.RawMessage) error {
			_, err := decodeCategoryID(raw)
			return err
		},
		Save: func(core plugincore.Core, book *models.Book, raw json.RawMessage) error {
			id, err := decodeCategoryID(raw)
			if err != nil {
				return err
			}
			return (&behavior{core: core}).assign([]uint{book.ID}, id)
		},
	})
	plugincore.OnBookCopied(func(core plugincore.Core, src, dst *models.Book) error {
		b := &behavior{core: core}
		if !b.queryable() {
			return nil
		}
		var a Assignment
		if core.Gorm().First(&a, src.ID).Error != nil {
			return nil
		}
		return b.assign([]uint{dst.ID}, a.CategoryID)
	})
	// /books?category=<slug>：该分类及其子分类下的书
	plugincore.RegisterBookFilter(func(core plugincore.Core, params url.Values, query *gorm.DB, bookIDColumn string) *gorm.DB {
		slug := strings.TrimSpace(params.Get("category"))
		b := &behavior{core: core}
		if slug == "" || !b.queryable() {
			return query
		}
		t := b.tree()
		cat, found := t.bySlug[slug]
		if !found {
			return query.Where("1 = 0")
		}
		return query.Where("EXISTS (SELECT 1 FROM book_category_assignments bca WHERE bca.book_id = "+bookIDColumn+" AND bca.category_id IN ?)", t.subtree(cat.ID))
	})
	plugincore.RegisterSitemapSource(func(core plugincore.Core) []plugincore.SitemapPage {
		b := &behavior{core: core}
		if !b.queryable() {
			return nil
		}
		var cats []Category
		core.Gorm().Order("id ASC").Find(&cats)
		out := make([]plugincore.SitemapPage, 0, len(cats))
		for _, c := range cats {
			out = append(out, plugincore.SitemapPage{Path: categoryPath(c.Slug), LastMod: c.UpdatedAt})
		}
		return out
	})
}

// categoryPath 分类在发现页的地址。
func categoryPath(slug string) string { return "/explore?category=" + url.QueryEscape(slug) }

func decodeCategoryID(raw json.RawMessage) (uint, error) {
	var id int64
	if err := json.Unmarshal(raw, &id); err != nil || id < 0 {
		return 0, errors.New("分类参数无效")
	}
	return uint(id), nil
}

// queryable 插件已启用且表存在。
func (b *behavior) queryable() bool {
	return b.core.PluginEnabled(pluginKey) && b.core.Gorm().Migrator().HasTable(&Assignment{})
}

// —— 分类树 ——

type tree struct {
	byID     map[uint]Category
	bySlug   map[string]Category
	children map[uint][]uint // 已按 sort_order、id 排序
}

func (b *behavior) tree() tree {
	var cats []Category
	b.core.Gorm().Order("sort_order ASC, id ASC").Limit(maxCategories).Find(&cats)
	t := tree{byID: map[uint]Category{}, bySlug: map[string]Category{}, children: map[uint][]uint{}}
	for _, c := range cats {
		t.byID[c.ID] = c
		t.bySlug[c.Slug] = c
		t.children[c.ParentID] = append(t.children[c.ParentID], c.ID)
	}
	return t
}

// path 从顶级分类到 id 的路径（含自身）；遇到缺失的父分类时截断。
func (t tree) path(id uint) []Category {
	var rev []Category
	for i := 0; i < maxDepth+2 && id != 0; i++ {
		c, found := t.byID[id]
		if !found {
			break
		}
		rev = append(rev, c)
		id = c.ParentID
	}
	out := make([]Category, len(rev))
	for i := range rev {
		out[i] = rev[len(rev)-1-i]
	}
	return out
}

// subtree id 及其全部子孙分类的 ID。
func (t tree) subtree(id uint) []uint {
	out := []uint{id}
	for i := 0; i < len(out) && len(out) <= maxCategories; i++ {
		out = append(out, t.children[out[i]]...)
	}
	return out
}

// height 以 id 为根的子树层数（叶子为 1）。
func (t tree) height(id uint) int {
	h := 0
	for _, child := range t.children[id] {
		if ch := t.height(child); ch > h {
			h = ch
		}
	}
	return h + 1
}

func ref(c Category) models.BookCategoryRef {
	return models.BookCategoryRef{ID: c.ID, Name: c.Name, Slug: c.Slug}
}

// attach 为一批书籍回填分类与路径。
func (b *behavior) attach(books []*models.Book) {
	if len(books) == 0 || !b.queryable() {
		return
	}
	ids := make([]uint, 0, len(books))
	for _, book := range books {
		ids = append(ids, book.ID)
	}
	var rows []Assignment
	if b.core.Gorm().Where("book_id IN ?", ids).Find(&rows).Error != nil || len(rows) == 0 {
		return
	}
	byBook := map[uint]uint{}
	for _, r := range rows {
		byBook[r.BookID] = r.CategoryID
	}
	t := b.tree()
	for _, book := range books {
		path := t.path(byBook[book.ID])
		if len(path) == 0 {
			continue
		}
		r := ref(path[len(path)-1])
		for _, p := range path {
			r.Path = append(r.Path, ref(p))
		}
		book.Category = &r
	}
}

// assign 把一批书归入分类（categoryID 为 0 时改为未分类）；插件禁用时跳过。
func (b *behavior) assign(bookIDs []uint, categoryID uint) error {
	if !b.queryable() || len(bookIDs) == 0 {
		return nil
	}
	db := b.core.Gorm()
	if categoryID != 0 {
		var n int64
		db.Model(&Category{}).Where("id = ?", categoryID).Count(&n)
		if n == 0 {
			return errors.New("分类不存在")
		}
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("book_id IN ?", bookIDs).Delete(&Assignment{}).Error; err != nil {
			return err
		}
		if categoryID == 0 {
			return nil
		}
		rows := make([]Assignment, 0, len(bookIDs))
		for _, id := range bookIDs {
			rows = append(rows, Assignment{BookID: id, CategoryID: categoryID})
		}
		return tx.CreateInBatches(rows, 200).Error
	})
}
