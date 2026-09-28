package booktranslations

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/models"
)

// 新建译本的预设（插件管理页配置）：按目标语言给出译本书名与访问路径的模板，新建译本时预填，作者可修改。
// 模板占位符：{title} 原书名、{language} 目标语言名称（如 简体中文）、{slug} 原书访问路径、{code} 语言代码（如 zh-cn）。
// 书名模板为空表示由 AI 翻译原书名；访问路径模板为空表示自动生成。未单独配置的语言使用默认模板。

const (
	cfgPresets       = "book_translations_presets"
	maxTemplateRunes = 200
	defaultSlugTpl   = "{slug}-{code}"
)

// translatePreset 一种语言（或默认）的模板。
type translatePreset struct {
	Title string `json:"title"`
	Slug  string `json:"slug"`
}

// translatePresets 默认模板与各语言的覆盖（键为语言代码，如 zh-CN）。
type translatePresets struct {
	Default   translatePreset            `json:"default"`
	Languages map[string]translatePreset `json:"languages"`
}

var (
	bookSlugRe  = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,196}[a-z0-9])?$`)
	slugInvalid = regexp.MustCompile(`[^a-z0-9]+`)
)

func (b *behavior) presets() translatePresets {
	p := translatePresets{Default: translatePreset{Slug: defaultSlugTpl}}
	if raw := b.core.GetSetting(cfgPresets); raw != "" {
		_ = json.Unmarshal([]byte(raw), &p)
	}
	if p.Languages == nil {
		p.Languages = map[string]translatePreset{}
	}
	return p
}

// presetFor 某语言生效的模板：该语言单独配置的项优先，未配置的项用默认模板。
func (p translatePresets) presetFor(code string) translatePreset {
	out := p.Default
	if l, ok := p.Languages[code]; ok {
		if l.Title != "" {
			out.Title = l.Title
		}
		if l.Slug != "" {
			out.Slug = l.Slug
		}
	}
	return out
}

// slugPart 转为访问路径片段：小写，非字母数字转为中划线。
func slugPart(s string) string {
	return strings.Trim(slugInvalid.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// render 按原书与目标语言展开模板。
func (t translatePreset) render(book *models.Book, code, label string) (title, slug string) {
	r := strings.NewReplacer("{title}", book.Title, "{language}", label, "{slug}", book.Slug, "{code}", strings.ToLower(code))
	title = strings.TrimSpace(r.Replace(t.Title))
	if t.Slug != "" {
		slug = slugPart(strings.NewReplacer("{title}", "", "{language}", "", "{slug}", book.Slug, "{code}", code).Replace(t.Slug))
		if len(slug) > 190 {
			slug = strings.Trim(slug[:190], "-")
		}
	}
	return title, slug
}

// slugTaken 访问路径是否已被使用（含回收站中的书籍）。
func (b *behavior) slugTaken(slug string, exceptID uint) bool {
	var n int64
	b.core.Gorm().Unscoped().Model(&models.Book{}).Where("slug = ? AND id <> ?", slug, exceptID).Count(&n)
	return n > 0
}

// availableSlug 预设路径被占用时追加数字后缀（-2、-3…），找不到则返回空（沿用自动生成的路径）。
func (b *behavior) availableSlug(base string) string {
	if base == "" {
		return ""
	}
	for i := 1; i <= 50; i++ {
		candidate := base
		if i > 1 {
			candidate = base + "-" + strconv.Itoa(i)
		}
		if bookSlugRe.MatchString(candidate) && !b.slugTaken(candidate, 0) {
			return candidate
		}
	}
	return ""
}

func validTemplate(t translatePreset) bool {
	return utf8.RuneCountInString(t.Title) <= maxTemplateRunes && utf8.RuneCountInString(t.Slug) <= maxTemplateRunes
}

// PresetForBook GET /books/:id/ai-translate/preset?lang=&label= 新建译本表单的预填值：按预设模板生成的书名与访问路径
// （访问路径已被占用时追加数字后缀）；书名为空表示由 AI 翻译原书名。
func (b *behavior) PresetForBook(c *gin.Context) {
	book, _, ok := b.sourceBook(c)
	if !ok {
		return
	}
	code, label := strings.TrimSpace(c.Query("lang")), strings.TrimSpace(c.Query("label"))
	if code == "" || len(code) > 16 || label == "" {
		b.core.Fail(c, http.StatusBadRequest, "请选择目标语言")
		return
	}
	title, slug := b.presets().presetFor(code).render(book, code, label)
	b.core.OK(c, gin.H{"title": truncate(title, 255), "slug": b.availableSlug(slug)})
}

// AdminGetPresets GET /admin/book-translations/presets 新建译本的预设模板。
func (b *behavior) AdminGetPresets(c *gin.Context) {
	b.core.OK(c, b.presets())
}

// AdminUpdatePresets PUT /admin/book-translations/presets {default:{title,slug}, languages:{code:{title,slug}}}
func (b *behavior) AdminUpdatePresets(c *gin.Context) {
	var req translatePresets
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	clean := translatePresets{Default: translatePreset{Title: strings.TrimSpace(req.Default.Title), Slug: strings.TrimSpace(req.Default.Slug)}, Languages: map[string]translatePreset{}}
	if !validTemplate(clean.Default) {
		b.core.Fail(c, http.StatusBadRequest, "模板不能超过 200 字")
		return
	}
	if len(req.Languages) > 100 {
		b.core.Fail(c, http.StatusBadRequest, "最多为 100 种语言单独设置")
		return
	}
	for code, t := range req.Languages {
		code = strings.TrimSpace(code)
		t = translatePreset{Title: strings.TrimSpace(t.Title), Slug: strings.TrimSpace(t.Slug)}
		if code == "" || len(code) > 16 || !validTemplate(t) {
			b.core.Fail(c, http.StatusBadRequest, "语言或模板无效")
			return
		}
		if t.Title != "" || t.Slug != "" {
			clean.Languages[code] = t
		}
	}
	raw, _ := json.Marshal(clean)
	if err := b.core.SetSetting(cfgPresets, string(raw), "书籍翻译：新建译本的书名与访问路径模板（JSON）"); err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	b.core.RecordAudit(c, "book_translations.presets_updated", "config", cfgPresets, "新建译本预设", map[string]any{"languages": len(clean.Languages)})
	b.core.OK(c, clean)
}
