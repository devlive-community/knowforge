package qa

import "time"

// Chunk 书籍内容分块（检索单元）：按 H2/H3 小节切分，过长的小节再按段落切分；Anchor 与阅读页标题锚点一致（h-N）。
type Chunk struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	BookID    uint   `gorm:"index" json:"book_id"`
	DocID     uint   `gorm:"index" json:"doc_id"`
	DocSlug   string `gorm:"size:255" json:"doc_slug"`
	DocTitle  string `gorm:"size:255" json:"doc_title"`
	Heading   string `gorm:"size:255" json:"heading"` // 小节标题（空表示章节开头）
	Anchor    string `gorm:"size:20" json:"anchor"`
	Ordinal   int    `json:"ordinal"` // 书内顺序
	Content   string `gorm:"type:text" json:"content"`
	Terms     string `gorm:"type:text" json:"-"` // 关键词检索用的预分词（空格分隔）
	Embedding []byte `json:"-"`                  // float32 小端序向量（未配置嵌入服务时为空）
}

func (Chunk) TableName() string { return "qa_chunks" }

// IndexState 书籍索引状态：ContentHash 为已发布章节（id+更新时间）的摘要，变化即需重建。
type IndexState struct {
	BookID      uint      `gorm:"primaryKey" json:"book_id"`
	ContentHash string    `gorm:"size:64" json:"-"`
	Chunks      int       `json:"chunks"`
	Embedded    int       `json:"embedded"`
	IndexedAt   time.Time `json:"indexed_at"`
	EmbedError  string    `gorm:"size:500" json:"embed_error"`
}

func (IndexState) TableName() string { return "qa_index_states" }

// Citation 回答中的出处。
type Citation struct {
	N        int    `json:"n"`
	DocID    uint   `json:"doc_id"`
	DocSlug  string `json:"doc_slug"`
	DocTitle string `json:"doc_title"`
	Heading  string `json:"heading"`
	Anchor   string `json:"anchor"`
	Snippet  string `json:"snippet"`
}

// Ask 一次 AI 问答（读者私有的问答记录）。
type Ask struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	BookID       uint      `gorm:"index" json:"book_id"`
	UserID       uint      `gorm:"index" json:"user_id"`
	DocID        uint      `json:"doc_id"`
	Mode         string    `gorm:"size:10" json:"mode"` // rag | agent
	Question     string    `gorm:"type:text" json:"question"`
	Selection    string    `gorm:"type:text" json:"selection"`
	Answer       string    `gorm:"type:text" json:"answer"`
	Citations    string    `gorm:"type:text" json:"-"` // JSON []Citation
	Steps        int       `json:"steps"`              // Agent 工具调用次数
	Calls        int       `json:"calls"`              // 模型调用次数
	InputTokens  int64     `json:"input_tokens"`       // 本次问答消耗（含向量化查询）
	OutputTokens int64     `json:"output_tokens"`
	Estimated    bool      `json:"estimated"`                     // 用量含估算值
	Status       string    `gorm:"size:10" json:"status"`         // running | done | failed | canceled
	Error        string    `gorm:"size:500" json:"error"`         // 面向读者的错误说明（不含 AI 服务原始报错）
	TraceID      string    `gorm:"size:40;index" json:"trace_id"` // 与核心 AI 用量记录的调用链 ID 一致
	Runner       string    `gorm:"size:32" json:"-"`              // 进行中：执行生成的服务实例（多实例部署）
	Trace        string    `gorm:"type:text" json:"-"`            // JSON []TraceStep 调用链
	DurationMs   int64     `json:"duration_ms"`
	QEmbedding   []byte    `json:"-"` // 问题向量（作者洞察归类用，后台计算）
	CreatedAt    time.Time `gorm:"index" json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (Ask) TableName() string { return "qa_asks" }

const (
	askRunning  = "running"
	askDone     = "done"
	askFailed   = "failed"
	askCanceled = "canceled"
)

// Question 社区提问（公开）；可附带提问者的 AI 回答作为参考。
type Question struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	BookID           uint      `gorm:"index" json:"book_id"`
	UserID           uint      `gorm:"index" json:"user_id"`
	DocID            uint      `json:"doc_id"`
	Title            string    `gorm:"size:200" json:"title"`
	Body             string    `gorm:"type:text" json:"body"`
	Selection        string    `gorm:"type:text" json:"selection"`
	AIAnswer         string    `gorm:"type:text" json:"ai_answer"`
	AICitations      string    `gorm:"type:text" json:"-"`
	AcceptedAnswerID uint      `json:"accepted_answer_id"`
	AnswerCount      int       `json:"answer_count"`
	Status           string    `gorm:"size:10;index" json:"status"`                         // open | resolved
	Visibility       string    `gorm:"size:10;index;not null;default:''" json:"visibility"` // ""（公开）| held（待审核）| hidden（驳回/下架）
	QEmbedding       []byte    `json:"-"`                                                   // 问题向量（作者洞察归类用）
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (Question) TableName() string { return "qa_questions" }

// Answer 社区回答。
type Answer struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	QuestionID uint      `gorm:"index" json:"question_id"`
	UserID     uint      `gorm:"index" json:"user_id"`
	Body       string    `gorm:"type:text" json:"body"`
	Visibility string    `gorm:"size:10;index;not null;default:''" json:"visibility"` // 同 Question.Visibility
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (Answer) TableName() string { return "qa_answers" }
