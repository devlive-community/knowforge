package booktranslations

import "time"

// 整本 AI 翻译的数据（插件独占表）。

// 任务状态与阶段。
const (
	jobRunning = "running"
	jobPaused  = "paused"
	jobDone    = "done"
	jobFailed  = "failed"

	stageOutline = "outline" // 翻译目录并建立译本章节
	stageContent = "content" // 逐章翻译正文
	stageDone    = "done"

	modeFull = "full" // 新建译本
	modeSync = "sync" // 同步原文的新增与修改

	itemPending = "pending"
	itemRunning = "running"
	itemDone    = "done"
	itemFailed  = "failed"
)

// TranslateJob 一次整本翻译（新建译本或同步更新）。
type TranslateJob struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	UserID       uint       `gorm:"index" json:"user_id"`
	SourceBookID uint       `gorm:"index" json:"source_book_id"`
	TargetBookID uint       `gorm:"index" json:"target_book_id"`
	TargetLang   string     `gorm:"size:16" json:"target_lang"`
	TargetLabel  string     `gorm:"size:64" json:"target_label"`
	Mode         string     `gorm:"size:10" json:"mode"`
	Stage        string     `gorm:"size:10" json:"stage"`
	Status       string     `gorm:"size:10;index" json:"status"`
	Instructions string     `gorm:"type:text" json:"instructions"`
	BookTitle    string     `gorm:"size:255" json:"-"` // 作者指定的译本书名（空则由 AI 翻译原书名）
	Total        int        `json:"total"`
	Done         int        `json:"done"`
	Failed       int        `json:"failed"`
	Chars        int64      `json:"chars"` // 已翻译的原文字符数（计入每月翻译字数）
	InputTokens  int64      `json:"input_tokens"`
	OutputTokens int64      `json:"output_tokens"`
	Estimated    bool       `json:"estimated"`
	TraceID      string     `gorm:"size:64" json:"trace_id"`
	Runner       string     `gorm:"size:32" json:"-"` // 进行中：执行翻译的服务实例（多实例部署）
	Error        string     `gorm:"size:500" json:"error"`
	FinishedAt   *time.Time `json:"finished_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

func (TranslateJob) TableName() string { return "book_ai_translate_jobs" }

// TranslateItem 任务中的一个章节。
type TranslateItem struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	JobID        uint      `gorm:"index" json:"job_id"`
	SourceDocID  uint      `json:"source_doc_id"`
	TargetDocID  uint      `json:"target_doc_id"`
	Ord          int       `json:"ord"`   // 处理顺序（目录先序：父章节在子章节之前）
	Depth        int       `json:"depth"` // 目录层级（0 为第一级）
	Title        string    `gorm:"size:255" json:"title"`
	TargetTitle  string    `gorm:"size:255" json:"target_title"`
	Status       string    `gorm:"size:10" json:"status"`
	Chars        int64     `json:"chars"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	DurationMs   int64     `json:"duration_ms"`
	Error        string    `gorm:"size:500" json:"error"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (TranslateItem) TableName() string { return "book_ai_translate_items" }

// TranslatedDoc 译本章节与原文章节的对应关系；SourceHash 为最近一次翻译时原文（标题 + 正文）的摘要，据此判断原文是否已修改。
type TranslatedDoc struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	TargetBookID uint      `gorm:"uniqueIndex:idx_ai_translated_doc" json:"target_book_id"`
	SourceDocID  uint      `gorm:"uniqueIndex:idx_ai_translated_doc" json:"source_doc_id"`
	TargetDocID  uint      `gorm:"index" json:"target_doc_id"`
	SourceHash   string    `gorm:"size:64" json:"source_hash"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (TranslatedDoc) TableName() string { return "book_ai_translated_docs" }

// GlossaryTerm 术语表（按原书 + 目标语言）：翻译时要求模型统一采用。
type GlossaryTerm struct {
	ID         uint   `gorm:"primaryKey" json:"id"`
	BookID     uint   `gorm:"index:idx_ai_glossary" json:"-"`
	TargetLang string `gorm:"size:16;index:idx_ai_glossary" json:"-"`
	Source     string `gorm:"size:200" json:"source"`
	Target     string `gorm:"size:200" json:"target"`
}

func (GlossaryTerm) TableName() string { return "book_ai_glossary_terms" }
