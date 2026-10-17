package contentcollect

import "time"

// CrawlJob 采集任务（整站/单页/单章）。由「内容采集」插件在启用时建表（表名 crawl_jobs 与迁移前一致）。
// Status: preview（仅生成目录预览，待用户确认）| pending | running | succeeded | partial | failed | canceled（用户停止）。
type CrawlJob struct {
	ID              uint       `gorm:"primaryKey" json:"id"`
	UserID          uint       `gorm:"index;not null" json:"user_id"`
	BookID          uint       `gorm:"index;not null;default:0" json:"book_id"`
	Kind            string     `gorm:"size:16;not null;index" json:"kind"` // site | page | chapter
	RootURL         string     `gorm:"size:1024;not null" json:"root_url"`
	ContentSelector string     `gorm:"size:255" json:"content_selector"` // 确认后的正文选择器；空=自动
	RenderMode      string     `gorm:"size:16;default:auto" json:"render_mode"`
	Status          string     `gorm:"size:16;not null;default:preview;index" json:"status"`
	PageLimit       int        `gorm:"default:200" json:"page_limit"`
	Total           int        `gorm:"default:0" json:"total"`
	Success         int        `gorm:"default:0" json:"success"`
	Failed          int        `gorm:"default:0" json:"failed"`
	LastError       string     `json:"last_error"`
	StartedAt       *time.Time `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`
	CreatedAt       time.Time  `gorm:"index" json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// CrawlPage 采集任务下的单个页面。用 ParentURL + SortOrder + Depth 还原书籍目录树。
type CrawlPage struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	JobID     uint      `gorm:"index;not null" json:"job_id"`
	URL       string    `gorm:"size:1024;not null" json:"url"`
	ParentURL string    `gorm:"size:1024" json:"parent_url"` // 空=顶级
	Title     string    `gorm:"size:512" json:"title"`
	Depth     int       `gorm:"default:0" json:"depth"`
	SortOrder int       `gorm:"default:0" json:"sort_order"`
	Status    string    `gorm:"size:16;not null;default:pending;index" json:"status"` // pending | running | success | failed | skipped
	Error     string    `json:"error"`
	DocID     uint      `gorm:"default:0" json:"doc_id"` // 采集成功后对应的章节 id
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
