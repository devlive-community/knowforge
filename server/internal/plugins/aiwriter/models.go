package aiwriter

import "time"

// 任务状态。
const (
	statusRunning  = "running"
	statusDone     = "done"
	statusFailed   = "failed"
	statusCanceled = "canceled"
)

// Task 一次写作助手任务（作者私有）：对选中的文字（或光标前的上文）执行一个动作，结果在后台流式生成。
type Task struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	UserID       uint       `gorm:"index" json:"user_id"`
	BookID       uint       `gorm:"index" json:"book_id"`
	DocID        uint       `gorm:"index" json:"doc_id"`
	Action       string     `gorm:"size:20" json:"action"`
	Instruction  string     `gorm:"type:text" json:"instruction"`
	Input        string     `gorm:"type:text" json:"input"` // 处理对象（续写时为光标前的上文）
	Result       string     `gorm:"type:text" json:"result"`
	Status       string     `gorm:"size:10;index" json:"status"`
	Error        string     `gorm:"size:500" json:"error"`
	TraceID      string     `gorm:"size:64" json:"trace_id"`
	Runner       string     `gorm:"size:32" json:"-"` // 进行中：执行生成的服务实例（多实例部署）
	Model        string     `gorm:"size:100" json:"model"`
	InputTokens  int64      `json:"input_tokens"`
	OutputTokens int64      `json:"output_tokens"`
	Estimated    bool       `json:"estimated"`
	DurationMs   int64      `json:"duration_ms"`
	Adopted      string     `gorm:"size:10" json:"adopted"` // 空 | replace | insert
	AdoptedAt    *time.Time `json:"adopted_at"`
	CreatedAt    time.Time  `gorm:"index" json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

func (Task) TableName() string { return "ai_writer_tasks" }
