package categories

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// 分类的多语言：名称与简介作为可翻译资源（kind=book_category），存取与语言回退由核心提供。
// 分类表中的 name / description 缓存默认语言已发布的内容（Sitemap、未本地化的内部调用使用）；
// 读者看到的分类树、分类页与书籍上的分类路径按请求语言替换为已发布的翻译，未翻译的语言沿用默认语言。

const resourceKind = "book_category"

func init() {
	plugincore.RegisterLocalizedResource(resourceKind, map[string]int{"name": maxName, "description": maxDesc})
	// 书籍详情 / 列表：把回填的分类路径换成请求语言
	plugincore.OnLocalizeBooks(func(core plugincore.Core, c *gin.Context, books []*models.Book) {
		(&behavior{core: core}).localizeBooks(c, books)
	})
}

// prepareTexts 规范化多语言内容：默认语言须有已发布的名称，并写回 req.Name / Description 作为缓存。
// 未传 translations 的旧调用方式按 name / description 作为默认语言（直接发布）。
func (b *behavior) prepareTexts(req *categoryPayload, id uint) error {
	def, err := b.core.DefaultContentLocale()
	if err != nil {
		return err
	}
	existing, err := b.core.LoadResourceTranslations(b.core.Gorm(), resourceKind, id)
	if err != nil {
		return err
	}
	if req.Translations == nil {
		req.Translations = map[string]plugincore.ResourceTranslation{}
		if strings.TrimSpace(req.Name) != "" {
			req.Translations[def] = plugincore.ResourceTranslation{
				Fields:   map[string]string{"name": strings.TrimSpace(req.Name), "description": strings.TrimSpace(req.Description)},
				Revision: existing[def].Revision, Publish: true,
			}
		}
	}
	published := existing[def].Published
	if tr, found := req.Translations[def]; found && tr.Publish {
		published = tr.Fields
	}
	if strings.TrimSpace(published["name"]) == "" {
		return errors.New("请填写并发布默认语言的分类名称")
	}
	req.Name = strings.TrimSpace(published["name"])
	req.Description = strings.TrimSpace(published["description"])
	return nil
}

// translations 一批分类的多语言内容（管理端编辑用）。
func (b *behavior) translations(ids []uint) map[uint]json.RawMessage {
	out := make(map[uint]json.RawMessage, len(ids))
	for _, id := range ids {
		if tr, err := b.core.LoadResourceTranslations(b.core.Gorm(), resourceKind, id); err == nil {
			out[id], _ = json.Marshal(tr)
		}
	}
	return out
}

// localized 按请求语言解析一批分类的名称与简介（未翻译的沿用默认语言缓存）。
func (b *behavior) localized(c *gin.Context, ids []uint) map[uint][2]string {
	out := map[uint][2]string{}
	if len(ids) == 0 {
		return out
	}
	resolved, _, err := b.core.LocalizeResources(c, resourceKind, ids)
	if err != nil {
		return out
	}
	for id, res := range resolved {
		var name, desc string
		found := false
		for _, layer := range res.Layers {
			if v := layer.Fields["name"]; v != "" {
				name, desc, found = v, layer.Fields["description"], true
			}
		}
		if found {
			out[id] = [2]string{name, desc}
		}
	}
	return out
}

// localizeTree 把分类树的名称与简介换成请求语言。
func (b *behavior) localizeTree(c *gin.Context, t tree) {
	ids := make([]uint, 0, len(t.byID))
	for id := range t.byID {
		ids = append(ids, id)
	}
	for id, texts := range b.localized(c, ids) {
		cat := t.byID[id]
		cat.Name, cat.Description = texts[0], texts[1]
		t.byID[id] = cat
		t.bySlug[cat.Slug] = cat
	}
}

// localizeBooks 把书籍上的分类路径换成请求语言。
func (b *behavior) localizeBooks(c *gin.Context, books []*models.Book) {
	if !b.queryable() {
		return
	}
	ids := []uint{}
	seen := map[uint]bool{}
	for _, book := range books {
		if book.Category == nil {
			continue
		}
		for _, p := range book.Category.Path {
			if !seen[p.ID] {
				seen[p.ID] = true
				ids = append(ids, p.ID)
			}
		}
	}
	names := b.localized(c, ids)
	if len(names) == 0 {
		return
	}
	for _, book := range books {
		if book.Category == nil {
			continue
		}
		for i := range book.Category.Path {
			if texts, found := names[book.Category.Path[i].ID]; found {
				book.Category.Path[i].Name = texts[0]
			}
		}
		if texts, found := names[book.Category.ID]; found {
			book.Category.Name = texts[0]
		}
	}
}
