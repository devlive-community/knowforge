package plugincore

import (
	"context"

	"knowforge/server/internal/models"
)

// —— 语义检索：插件（如问答插件基于其向量索引）为核心的站内搜索与相关推荐提供语义匹配。——
//
// 核心只经此接口询问，不认识具体插件；返回的书籍/章节仍由核心按当前用户的可见性与内容门禁再次过滤。
// 未登记或不可用时，核心的语义搜索返回空、相关推荐回退到其他方式（如同标签书籍）。

// SemanticHit 一条语义命中（章节中的一个小节）。
type SemanticHit struct {
	BookID  uint
	DocID   uint
	Heading string // 小节标题（空表示章节开头）
	Anchor  string // 阅读页标题锚点（h-N）
	Snippet string
	Score   float64
}

// SemanticProvider 语义检索能力。
type SemanticProvider struct {
	// Available 当前是否可用（插件启用、已开启全站语义检索且嵌入模型可用）。
	Available func(core Core) bool
	// Allowed 用户能否使用语义搜索（如权益；nil 表示所有人可用）。相关推荐不受此限制。
	Allowed func(core Core, u *models.User) bool
	// Search 在 u 可读的内容中检索与 query 语义相关的小节（bookID 非 0 时限定在该书内），按相关度降序。
	Search func(ctx context.Context, core Core, u *models.User, query string, bookID uint, limit int) ([]SemanticHit, error)
	// RelatedBooks 与 book 内容相近的其他书籍 ID（按相关度降序）。
	RelatedBooks func(ctx context.Context, core Core, u *models.User, book *models.Book, limit int) []uint
	// RelatedDocs 与 doc 内容相近的其他章节 ID（按相关度降序）。
	RelatedDocs func(ctx context.Context, core Core, u *models.User, doc *models.Document, limit int) []uint
}

var semanticProviders []SemanticProvider

// RegisterSemanticProvider 登记语义检索能力。
func RegisterSemanticProvider(p SemanticProvider) { semanticProviders = append(semanticProviders, p) }

// ActiveSemanticProvider 第一个当前可用的语义检索能力。
func ActiveSemanticProvider(core Core) (SemanticProvider, bool) {
	for _, p := range semanticProviders {
		if p.Available != nil && p.Available(core) {
			return p, true
		}
	}
	return SemanticProvider{}, false
}
