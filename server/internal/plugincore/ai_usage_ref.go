package plugincore

import (
	"strings"

	"knowforge/server/internal/models"
)

// —— AI 调用记录的关联对象：调用记录（ai_usage_logs）保存了功能名、关联类型/ID 与调用链 ID，
// 各功能所属的插件登记解析器，把它们解析为可展示的名称与跳转地址（如翻译任务的进度页、书籍问答），
// 用户的「我的 AI 用量」与管理员的调用明细据此显示「关联」。——

// AIUsageRefInput 一条调用记录的关联信息。
type AIUsageRefInput struct {
	Feature string
	RefType string
	RefID   uint
	TraceID string
}

// AIUsageRef 关联对象的展示信息；Link 为站内路径。
type AIUsageRef struct {
	Kind  string `json:"kind"` // 如 book、translation、qa、moderation_case（前端据此显示前缀文案）
	Title string `json:"title"`
	Link  string `json:"link"`
}

// AIUsageRefResolver 解析关联对象；viewer 为查看者（管理员或调用者本人），不可见或无法解析时返回 nil。
type AIUsageRefResolver func(core Core, viewer *models.User, in AIUsageRefInput) *AIUsageRef

type aiUsageRefEntry struct {
	prefix  string
	resolve AIUsageRefResolver
}

var aiUsageRefResolvers []aiUsageRefEntry

// RegisterAIUsageRef 为功能名以 featurePrefix 开头的调用记录登记解析器（最长前缀优先）。
func RegisterAIUsageRef(featurePrefix string, fn AIUsageRefResolver) {
	aiUsageRefResolvers = append(aiUsageRefResolvers, aiUsageRefEntry{prefix: featurePrefix, resolve: fn})
}

// ResolveAIUsageRef 按功能名找最长前缀匹配的解析器；都没有或解析失败时回退：关联书籍显示书名并链接到书籍详情。
func ResolveAIUsageRef(core Core, viewer *models.User, in AIUsageRefInput) *AIUsageRef {
	var best *aiUsageRefEntry
	for i := range aiUsageRefResolvers {
		e := &aiUsageRefResolvers[i]
		if strings.HasPrefix(in.Feature, e.prefix) && (best == nil || len(e.prefix) > len(best.prefix)) {
			best = e
		}
	}
	if best != nil {
		if ref := best.resolve(core, viewer, in); ref != nil {
			return ref
		}
	}
	if in.RefType == "book" && in.RefID > 0 {
		if book := ReadableBook(core, viewer, in.RefID); book != nil {
			return &AIUsageRef{Kind: "book", Title: book.Title, Link: "/book/detail/" + book.Slug}
		}
	}
	return nil
}

// ReadableBook 查看者可读的书籍（管理员可见全部）；不存在或不可读返回 nil。
func ReadableBook(core Core, viewer *models.User, id uint) *models.Book {
	var book models.Book
	if id == 0 || core.Gorm().First(&book, id).Error != nil {
		return nil
	}
	if !core.IsAdmin(viewer) && !core.CanReadBook(viewer, &book) {
		return nil
	}
	return &book
}
