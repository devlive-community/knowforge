// Package plugincore 定义插件与核心之间的「服务接口」与「行为注册/钩子」，
// 让插件的 handler 可以搬到自己的子包（internal/plugins/<name>/）而不与 app 包产生循环依赖：
//   - Core：插件从核心获得的能力集合（*app.App 实现之，按需扩展方法）。
//   - Plugin：插件的行为（注册路由/钩子）；子包在 init() 里 RegisterBehavior 自注册，app 只遍历。
//   - 钩子：核心在关键点（如章节发布）触发，插件按需订阅——替代原先核心直接调用某插件方法。
package plugincore

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"knowforge/server/internal/ai"
	"knowforge/server/internal/authz"
	"knowforge/server/internal/jobqueue"
	"knowforge/server/internal/models"
)

// Core 插件从核心获得的能力（*app.App 实现）。按迁移需要逐步扩展，保持与原 app 内部函数一一对应。
type Core interface {
	Gorm() *gorm.DB // GORM 句柄（避免与 *App.DB 字段同名，命名为 Gorm）
	CurrentUser(c *gin.Context) *models.User
	OK(c *gin.Context, data any)
	Fail(c *gin.Context, status int, message string)
	AtoiDefault(s string, def int) int
	Notify(userID uint, ntype, title string, payload map[string]any)
	// NotifyI18n 以可翻译文本发送通知：key 为 i18n 键（模板经 i18ntext.Register 登记），params 为插值参数。
	NotifyI18n(userID uint, ntype, key string, params map[string]string, payload map[string]any)
	PluginEnabled(key string) bool
	RecordAudit(c *gin.Context, action, resourceType, resourceID, label string, summary map[string]any)

	// 书籍/用户领域共享工具（核心与多个插件复用）
	IsAdmin(u *models.User) bool
	CanReadBook(u *models.User, b *models.Book) bool
	FindBook(c *gin.Context) (*models.Book, int)
	PreloadBookUser() *gorm.DB
	PreloadBookUserOn(db *gorm.DB) *gorm.DB
	AttachChapterCounts(books []models.Book)
	// DecorateBookList 让插件回填列表书籍的非持久化字段（标签、「采集中」标记等），见 OnDecorateBooks。
	DecorateBookList(books []models.Book)
	PubliclyReadableBookStatuses() []string

	// 路由中间件（返回 gin.HandlerFunc，供插件注册受保护路由）
	RequireAuth() gin.HandlerFunc
	// RequireAuthStream 同 RequireAuth，另接受 ?token= 查询参数（供 EventSource 事件流接口使用）。
	RequireAuthStream() gin.HandlerFunc
	OptionalAuth() gin.HandlerFunc
	RequirePermission(perm authz.Permission) gin.HandlerFunc
	RequireFeaturePlugin(key string) gin.HandlerFunc
	RequireEmailVerified() gin.HandlerFunc
	RequireAdmin() gin.HandlerFunc
	RequirePermissionMiddleware(perm authz.Permission) gin.HandlerFunc // 与 RequirePermission 同义（命名区分，供插件显式使用）
	RateLimitReaction() gin.HandlerFunc                                // 互动类写操作的限流中间件（关注/点赞等复用同一策略）

	// 通用工具
	Paginate(c *gin.Context) (page, pageSize int)
	Slugify(s string) string
	RandomSlug(prefix string) string

	// 内容采集插件所需（引擎/worker/端点搬入子包后经此访问核心）
	CanEditBookContent(u *models.User, b *models.Book) bool
	GetSetting(key string) string
	UniqueChildSlug(bookID uint, parentID *uint, base string, excludeID uint) string
	InstalledChromePath() string
	CreateContentImportBook(u *models.User, title, description string, chapters []ImportedChapter) (models.Book, error)
	// CreateDraftBookFrom 以 src 的配置为模板为 u 新建一本私有草稿书（不含章节；校验书籍数量权益，复制导出/水印/标签等设置）。
	CreateDraftBookFrom(u *models.User, src *models.Book, title string) (models.Book, error)
	JobQueue() *jobqueue.Queue
	// InitialChapterStatus 未显式指定状态时新章节的初始状态（子章节跟随父章节 / 第一级用书籍默认状态 / 否则草稿）。
	InitialChapterStatus(book *models.Book, parentID *uint) string
	// NewDocumentRevision 为章节生成一条版本记录（调用方负责在事务内保存）。
	NewDocumentRevision(doc *models.Document, userID uint, reason string) models.DocumentRevision
	// ExtractDocIcon 从章节正文提取图标声明（与编辑器保存章节时的规则一致）。
	ExtractDocIcon(content string) string

	// 成就插件所需
	SetSetting(key, value, description string) error
	// LoadResourceTranslations / SaveResourceTranslations 读写可翻译资源（字段白名单见 RegisterLocalizedResource）。
	LoadResourceTranslations(db *gorm.DB, kind string, id uint) (map[string]ResourceTranslation, error)
	SaveResourceTranslations(tx *gorm.DB, kind string, id, actor uint, translations map[string]ResourceTranslation) error
	// DefaultContentLocale 站点默认语言代码。
	DefaultContentLocale() (string, error)
	// LocalizeResources 按请求语言回退链解析资源的已发布翻译（并设置 Content-Language 等响应头），返回请求语言代码。
	LocalizeResources(c *gin.Context, kind string, ids []uint) (map[uint]LocalizedResource, string, error)
	// AIChat / AIEmbed 调用站点「AI 服务」（对话支持工具调用；嵌入为 OpenAI 兼容）；AIStatus 两者是否已配置。
	AIChat(ctx context.Context, req ai.ChatRequest) (ai.ChatResponse, error)
	AIEmbed(ctx context.Context, texts []string) ([][]float32, ai.Usage, error)
	// AIChatStream 同 AIChat，并流式回调生成的文本片段（onDelta 在调用方的 goroutine 中同步调用）。
	AIChatStream(ctx context.Context, req ai.ChatRequest, onDelta func(text string)) (ai.ChatResponse, error)
	AIStatus() (chat, embed bool)
	// AISpeech 调用站点「AI 服务」中的语音合成（OpenAI 兼容），按字符数记录用量（调用方见 ai.WithCaller）；
	// AISpeechInfo 返回语音合成模型与可选音色（第一个为默认），未配置时 model 为空。
	AISpeech(ctx context.Context, req ai.SpeechRequest) (ai.SpeechResult, error)
	AISpeechInfo() (model string, voices []string)
	// AITranslateStream 同 AIChatStream，并按原文字符数 chars 记入翻译用量（调用方功能须为 translate.*，计入每月翻译字数权益）。
	AITranslateStream(ctx context.Context, req ai.ChatRequest, chars int64, onDelta func(text string)) (ai.ChatResponse, error)
	// TranslateCharsLeft 用户本月剩余翻译字数（-1 为不限）。
	TranslateCharsLeft(u *models.User) int64
	// AICheckQuota 按 ctx 上标注的调用方（ai.WithCaller）判定每月 AI 用量额度，超出返回 ai.ErrQuotaExceeded；
	// 多次调用的功能可在开始前调用，避免中途失败。
	AICheckQuota(ctx context.Context) error
	// GuardDocumentPublish 已写入且为已发布状态的新章节交发布守卫审查，被拦截时改回草稿；返回拦截说明（放行为空）。
	GuardDocumentPublish(book *models.Book, doc *models.Document, actorID uint) string
	// ApplyModeration 审核结论落地（绕过发布守卫）：approve 应用 requested（发布章节/公开书籍），否则撤回发布。
	ApplyModeration(kind string, id uint, approve bool, requested map[string]string) error
	// EnsureBookQuota 校验用户是否还能创建书籍（书籍数量权益）；超限返回可展示的错误。
	EnsureBookQuota(u *models.User) error
	// PublicBackgroundJob 后台任务的对外视图（不含 payload），与 /tasks/:id 返回形状一致。
	PublicBackgroundJob(job *models.BackgroundJob) any

	// 导出（PDF 导出插件所需；与 DOCX/EPUB 等核心导出共用同一套规则）
	CanExportBook(u *models.User, book *models.Book) bool
	ExportFormatAllowed(book *models.Book, format string) bool
	ResolveExportStyle(style string, book *models.Book, u *models.User) models.UserExportSetting
	ResolveExportFooter(book *models.Book, u *models.User) string
	RecordBookExport(u *models.User, book *models.Book, format string)
	// PrivateDataDir 数据目录下供插件存放私有文件（如缓存）的子目录（已创建）；不经对象存储、不对外公开，不进入备份。
	PrivateDataDir(name string) (string, error)
	// WebPort 内嵌 Web 运行时的本地端口（未运行为 0），供无头浏览器访问打印页。
	WebPort() int
}

// ImportedChapter 导入/采集成书时的中性章节结构（避免暴露 app 内部类型）。
type ImportedChapter struct {
	Title   string
	Content string
}

// PageResult 分页响应（核心与插件共用，保持 JSON 形状一致）。
type PageResult struct {
	Items    any   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

// Plugin 插件的行为：注册路由与钩子。核心在构建路由时调用 RegisterRoutes。
type Plugin interface {
	Key() string
	RegisterRoutes(api *gin.RouterGroup, core Core)
}

var behaviors []Plugin

// RegisterBehavior 供各插件子包在 init() 中自注册行为。
func RegisterBehavior(p Plugin) { behaviors = append(behaviors, p) }

// Behaviors 返回全部已注册的插件行为（注册顺序）。
func Behaviors() []Plugin { return behaviors }

// —— 钩子：核心在关键点触发，插件订阅（解耦核心对具体插件的直接调用）——

// ChapterPublishedHook 章节发布后的回调（core 为发起调用的核心实例）。
type ChapterPublishedHook func(core Core, book *models.Book, doc *models.Document)

var chapterPublishedHooks []ChapterPublishedHook

// OnChapterPublished 订阅章节发布事件。
func OnChapterPublished(h ChapterPublishedHook) {
	chapterPublishedHooks = append(chapterPublishedHooks, h)
}

// FireChapterPublished 由核心在章节发布成功后调用，依次通知订阅者。
func FireChapterPublished(core Core, book *models.Book, doc *models.Document) {
	for _, h := range chapterPublishedHooks {
		h(core, book, doc)
	}
}

var chapterChangedHooks []ChapterPublishedHook

// OnChapterContentChanged 订阅章节标题或正文被保存修改（任意状态，插件按需判断是否已发布）。
func OnChapterContentChanged(h ChapterPublishedHook) {
	chapterChangedHooks = append(chapterChangedHooks, h)
}

// FireChapterContentChanged 由核心在章节标题或正文保存修改后调用。
func FireChapterContentChanged(core Core, book *models.Book, doc *models.Document) {
	for _, h := range chapterChangedHooks {
		h(core, book, doc)
	}
}

// —— 插件后台任务：任务队列可能在安装向导完成后才创建（且会重建），故插件只登记「处理器工厂」，
// 由核心在每次创建队列时统一注册。——

// JobHandlerFactory 以核心能力构造任务处理器。
type JobHandlerFactory func(core Core) func(ctx context.Context, raw json.RawMessage) error

// PluginJob 已登记的插件后台任务。
type PluginJob struct {
	Type    string
	Factory JobHandlerFactory
}

var pluginJobs []PluginJob

// RegisterJob 供插件子包在 init() 中登记后台任务类型。
func RegisterJob(jobType string, factory JobHandlerFactory) {
	pluginJobs = append(pluginJobs, PluginJob{Type: jobType, Factory: factory})
}

// Jobs 返回全部已登记的插件后台任务（登记顺序）。
func Jobs() []PluginJob { return pluginJobs }

// —— 书籍装饰钩子：核心返回书籍（列表/详情）前调用，插件按需回填非持久化字段（如「采集中」标记）。——

// BooksDecorator 书籍装饰回调；core 为发起调用的核心实例，books 为本次响应中的书籍（可原地修改）。
type BooksDecorator func(core Core, books []*models.Book)

var booksDecorators []BooksDecorator

// OnDecorateBooks 订阅书籍装饰。
func OnDecorateBooks(h BooksDecorator) { booksDecorators = append(booksDecorators, h) }

// DecorateBooks 由核心在返回书籍前调用。
func DecorateBooks(core Core, books []*models.Book) {
	if len(books) == 0 {
		return
	}
	for _, h := range booksDecorators {
		h(core, books)
	}
}

// —— 业务活动事件：核心在业务操作成功后发出（注册、建书、评论、阅读等），插件订阅（如成就评估）。——

// ActivityEvent 一次业务活动；DedupeKey 用于订阅方幂等。
type ActivityEvent struct {
	UserID     uint
	Type       string
	SourceType string
	SourceID   string
	DedupeKey  string
	// Data 可选的事件详情（如售出金额、提问标题），供需要对外转发的订阅方使用（如 Webhook），
	// 使其无需依赖发出方插件的数据模型；其他订阅方可忽略。
	Data map[string]any
}

// ActivityHandler 活动订阅回调；失败不得反向影响主业务。
type ActivityHandler func(core Core, ev ActivityEvent)

var activityHandlers []ActivityHandler

// OnActivity 订阅业务活动事件。
func OnActivity(h ActivityHandler) { activityHandlers = append(activityHandlers, h) }

// FireActivity 由核心在业务操作成功后调用。
func FireActivity(core Core, ev ActivityEvent) {
	if ev.UserID == 0 {
		return
	}
	for _, h := range activityHandlers {
		h(core, ev)
	}
}

// —— 任务队列巡检：核心在任务队列创建时及之后的周期巡检（与维护任务同频）时调用，插件可补投遗留任务。——

var queueSweepHooks []func(core Core, queue *jobqueue.Queue)

// OnJobQueueSweep 订阅任务队列巡检。
func OnJobQueueSweep(h func(core Core, queue *jobqueue.Queue)) {
	queueSweepHooks = append(queueSweepHooks, h)
}

// FireJobQueueSweep 由核心在任务队列创建及周期巡检时调用。
func FireJobQueueSweep(core Core, queue *jobqueue.Queue) {
	for _, h := range queueSweepHooks {
		h(core, queue)
	}
}

// —— 插件启用：插件在管理端被启用后的初始化（如成就全量重算）。——

var pluginEnabledHooks = map[string][]func(core Core) error{}

// OnPluginEnabled 订阅某插件被启用。
func OnPluginEnabled(key string, h func(core Core) error) {
	pluginEnabledHooks[key] = append(pluginEnabledHooks[key], h)
}

// PluginEnabledHooks 返回某插件的启用回调。
func PluginEnabledHooks(key string) []func(core Core) error { return pluginEnabledHooks[key] }

// —— 用户数据：插件声明按 user_id 归属的表，核心删除用户（注销/管理员删除）时一并清理（表不存在则跳过）。——

var userDataModels []any

// RegisterUserDataModels 登记插件的用户归属模型（需含 user_id 列）。
func RegisterUserDataModels(models ...any) { userDataModels = append(userDataModels, models...) }

// UserDataModels 返回全部已登记的用户归属模型。
func UserDataModels() []any { return userDataModels }

// —— 可翻译资源：插件登记资源类型的字段白名单（字段 → 最大字符数），核心负责存取与语言回退。——

// ResourceTranslation 一种语言下的资源翻译（草稿 Fields / 已发布 Published，Revision 乐观锁）。
type ResourceTranslation struct {
	Fields    map[string]string `json:"fields"`
	Published map[string]string `json:"published,omitempty"`
	Revision  int               `json:"revision"`
	Publish   bool              `json:"publish"`
}

// LocalizedLayer 语言回退链上的一层已发布翻译。
type LocalizedLayer struct {
	Locale string
	Fields map[string]string
}

// LocalizedResource 资源的已发布翻译：Layers 按「兜底语言 → 请求语言」顺序排列，依次覆盖即得最终文案。
type LocalizedResource struct {
	HasTranslations bool
	Layers          []LocalizedLayer
}

// ErrTranslationConflict 翻译被并发修改（Revision 不匹配）。
var ErrTranslationConflict = errors.New("翻译已被其他操作更新，请重新加载")

var localizedResourceFields = map[string]map[string]int{}

// RegisterLocalizedResource 登记可翻译资源类型及其字段白名单。
func RegisterLocalizedResource(kind string, fields map[string]int) {
	localizedResourceFields[kind] = fields
}

// LocalizedResourceFields 返回资源类型的字段白名单。
func LocalizedResourceFields(kind string) (map[string]int, bool) {
	f, ok := localizedResourceFields[kind]
	return f, ok
}

// —— 经验记账服务：由成长插件提供，其他插件（如成就解锁奖励）通过 RecordExperience 调用；未提供时为空操作。——

// ExperienceRecorder 幂等记一条经验流水（dedupeKey 唯一）并重算成长资料。
type ExperienceRecorder func(core Core, userID uint, ruleKey, sourceType, sourceID, dedupeKey string, xp int, reason string)

var experienceRecorder ExperienceRecorder

// ProvideExperienceRecorder 由成长插件登记经验记账实现。
func ProvideExperienceRecorder(f ExperienceRecorder) { experienceRecorder = f }

// RecordExperience 记一条经验流水（成长插件未提供或禁用时为空操作）。
func RecordExperience(core Core, userID uint, ruleKey, sourceType, sourceID, dedupeKey string, xp int, reason string) {
	if experienceRecorder != nil {
		experienceRecorder(core, userID, ruleKey, sourceType, sourceID, dedupeKey, xp, reason)
	}
}

// —— 书籍扩展字段：插件登记书籍创建/更新请求体中的额外字段（如 tags），核心负责校验与保存时机。——

// BookField 书籍扩展字段。Validate 在保存前校验原始 JSON（失败按参数错误处理）；
// Save 在书籍创建/更新成功后调用（字段出现且非 null 时），返回的错误原样作为失败提示。
type BookField struct {
	Name     string
	Validate func(raw json.RawMessage) error
	Save     func(core Core, book *models.Book, raw json.RawMessage) error
}

var bookFields []BookField

// RegisterBookField 登记书籍扩展字段。
func RegisterBookField(f BookField) { bookFields = append(bookFields, f) }

// BookFields 返回全部已登记的书籍扩展字段。
func BookFields() []BookField { return bookFields }

// —— 书籍复制：核心复制书籍后调用，插件复制自身关联数据（如标签）。——

// BookCopiedHook 书籍复制回调（src 源书，dst 新书）。
type BookCopiedHook func(core Core, src, dst *models.Book) error

var bookCopiedHooks []BookCopiedHook

// OnBookCopied 订阅书籍复制。
func OnBookCopied(h BookCopiedHook) { bookCopiedHooks = append(bookCopiedHooks, h) }

// FireBookCopied 由核心在复制书籍后调用；插件失败不影响复制结果。
func FireBookCopied(core Core, src, dst *models.Book) {
	for _, h := range bookCopiedHooks {
		_ = h(core, src, dst)
	}
}

// —— 书籍筛选：书籍列表/搜索按请求参数追加插件提供的条件（如 ?tag=slug）。——

// BookFilter 按 params 为 query 追加条件；bookIDColumn 为查询中书籍 id 列（如 books.id / b.id）。
type BookFilter func(core Core, params url.Values, query *gorm.DB, bookIDColumn string) *gorm.DB

var bookFilters []BookFilter

// RegisterBookFilter 登记书籍筛选条件。
func RegisterBookFilter(f BookFilter) { bookFilters = append(bookFilters, f) }

// ApplyBookFilters 由核心在构造书籍列表/搜索查询时调用。
func ApplyBookFilters(core Core, params url.Values, query *gorm.DB, bookIDColumn string) *gorm.DB {
	for _, f := range bookFilters {
		query = f(core, params, query, bookIDColumn)
	}
	return query
}

// —— 站点统计：插件为管理端/公开统计补充字段（如 tag_count）。——

// StatsProvider 返回要合并进统计响应的字段；publicOnly 为公开统计（仅计公开可读内容）。
type StatsProvider func(core Core, publicOnly bool) map[string]any

var statsProviders []StatsProvider

// RegisterStatsProvider 登记统计字段提供者。
func RegisterStatsProvider(f StatsProvider) { statsProviders = append(statsProviders, f) }

// CollectStats 把各插件提供的统计字段合并进 into。
func CollectStats(core Core, publicOnly bool, into map[string]any) {
	for _, f := range statsProviders {
		for k, v := range f(core, publicOnly) {
			into[k] = v
		}
	}
}

// —— 书籍数据：插件声明按 book_id 归属的表，核心彻底删除书籍/注销用户时一并清理（表不存在则跳过）。——

var bookDataModels []any

// RegisterBookDataModels 登记插件的书籍归属模型（需含 book_id 列）。
func RegisterBookDataModels(models ...any) { bookDataModels = append(bookDataModels, models...) }

// BookDataModels 返回全部已登记的书籍归属模型。
func BookDataModels() []any { return bookDataModels }

// ExperienceRevoker 收回某用户由某来源（规则键 + 来源 ID）获得、尚未收回的经验（记等额负流水）。
type ExperienceRevoker func(core Core, userID uint, ruleKey, sourceID, reason string)

var experienceRevoker ExperienceRevoker

// ProvideExperienceRevoker 由成长插件登记经验收回实现。
func ProvideExperienceRevoker(f ExperienceRevoker) { experienceRevoker = f }

// RevokeExperience 收回经验（成长插件未提供或禁用时为空操作）；如成就被撤销时收回其奖励经验。
func RevokeExperience(core Core, userID uint, ruleKey, sourceID, reason string) {
	if experienceRevoker != nil {
		experienceRevoker(core, userID, ruleKey, sourceID, reason)
	}
}

// —— 公开站点配置：插件为 GET /site 补充不敏感的配置项（如某功能是否开放），供前端联动入口显示。——

// SiteConfigProvider 返回要合并进公开站点配置的键值。
type SiteConfigProvider func(core Core) map[string]any

var siteConfigProviders []SiteConfigProvider

// RegisterPublicSiteConfig 登记公开站点配置提供者。
func RegisterPublicSiteConfig(f SiteConfigProvider) {
	siteConfigProviders = append(siteConfigProviders, f)
}

// CollectPublicSiteConfig 把各插件提供的公开配置合并进 into。
func CollectPublicSiteConfig(core Core, into map[string]any) {
	for _, f := range siteConfigProviders {
		for k, v := range f(core) {
			into[k] = v
		}
	}
}

// ExperienceGranter 确保某用户由某来源（规则键 + 来源 ID）持有一份经验：该来源当前净经验 > 0 时不重复发放，
// 否则发放一份（被收回后可再次发放）。用于「每个来源只能有一份」的奖励，如成就解锁奖励。
type ExperienceGranter func(core Core, userID uint, ruleKey, sourceType, sourceID string, xp int, reason string)

var experienceGranter ExperienceGranter

// ProvideExperienceGranter 由成长插件登记「只发一份」的经验发放实现。
func ProvideExperienceGranter(f ExperienceGranter) { experienceGranter = f }

// GrantExperienceOnce 为来源发放唯一一份经验（成长插件未提供或禁用时为空操作）。
func GrantExperienceOnce(core Core, userID uint, ruleKey, sourceType, sourceID string, xp int, reason string) {
	if experienceGranter != nil {
		experienceGranter(core, userID, ruleKey, sourceType, sourceID, xp, reason)
	}
}

// —— 经验服务就绪：成长插件启用并完成初始化后调用，供其他插件对账（如补发/收回停用期间的成就奖励）。——

var experienceReadyHooks []func(core Core)

// OnExperienceReady 订阅经验服务就绪。
func OnExperienceReady(h func(core Core)) { experienceReadyHooks = append(experienceReadyHooks, h) }

// FireExperienceReady 由成长插件在启用初始化完成后调用。
func FireExperienceReady(core Core) {
	for _, h := range experienceReadyHooks {
		h(core)
	}
}
