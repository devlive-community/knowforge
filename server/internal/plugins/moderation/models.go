package moderation

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// Word 敏感词。
type Word struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Word      string    `gorm:"size:100;uniqueIndex;not null" json:"word"`
	Category  string    `gorm:"size:30;index" json:"category"`
	Enabled   bool      `gorm:"not null" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

func (Word) TableName() string { return "moderation_words" }

// 审核记录状态。
const (
	StatusAutoPassed = "auto_passed" // 自动审核通过并已发布（管理员可复审）
	StatusPending    = "pending"     // 命中敏感词，待人工审核
	StatusApproved   = "approved"    // 人工复审通过
	StatusRejected   = "rejected"    // 人工复审驳回
)

// Hits 命中列表（JSON 存储）。
type Hits []Hit

func (h Hits) Value() (driver.Value, error) {
	if len(h) == 0 {
		return "[]", nil
	}
	raw, err := json.Marshal([]Hit(h))
	return string(raw), err
}

func (h *Hits) Scan(src any) error { return scanJSON(src, h) }

// StringMap 字符串键值（JSON 存储）。
type StringMap map[string]string

func (m StringMap) Value() (driver.Value, error) {
	raw, err := json.Marshal(map[string]string(m))
	return string(raw), err
}

func (m *StringMap) Scan(src any) error { return scanJSON(src, m) }

func scanJSON(src any, dst any) error {
	var raw []byte
	switch v := src.(type) {
	case nil:
		return nil
	case string:
		raw = []byte(v)
	case []byte:
		raw = v
	default:
		return fmt.Errorf("不支持的类型 %T", src)
	}
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, dst)
}

// Case 一次审核记录：每个对象（章节/书籍）同一时间最多一条「未结」记录（自动通过或待审核），再次提交时更新该记录。
type Case struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	Kind       string     `gorm:"size:20;index:idx_moderation_target" json:"kind"` // document | book
	TargetID   uint       `gorm:"index:idx_moderation_target" json:"target_id"`
	BookID     uint       `gorm:"index" json:"book_id"`
	UserID     uint       `gorm:"index" json:"user_id"` // 作者
	Title      string     `gorm:"size:255" json:"title"`
	Status     string     `gorm:"size:20;index" json:"status"`
	Hits       Hits       `gorm:"type:text" json:"hits"`
	Requested  StringMap  `gorm:"type:text" json:"-"`
	ReviewerID uint       `json:"reviewer_id"`
	ReviewNote string     `gorm:"size:500" json:"review_note"`
	ReviewedAt *time.Time `json:"reviewed_at"`
	Snapshot   StringMap  `gorm:"type:text" json:"-"` // 提交审查时的内容（AI 复核依据）
	// AI 复核（作者不可见）
	AIStatus       string     `gorm:"size:10" json:"ai_status"`        // 空 | queued | reviewing | done | failed
	AIVerdict      string     `gorm:"size:12;index" json:"ai_verdict"` // safe | violation | uncertain
	AIConfidence   float64    `json:"ai_confidence"`
	AICategories   StringList `gorm:"type:text" json:"ai_categories"`
	AIReason       string     `gorm:"size:500" json:"ai_reason"`
	AIError        string     `gorm:"size:200" json:"ai_error"`
	AIModel        string     `gorm:"size:100" json:"ai_model"`
	AIInputTokens  int64      `json:"ai_input_tokens"`
	AIOutputTokens int64      `json:"ai_output_tokens"`
	AIReviewedAt   *time.Time `json:"ai_reviewed_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// StringList 以 JSON 存储的字符串列表。
type StringList []string

func (l StringList) Value() (driver.Value, error) {
	if l == nil {
		l = StringList{}
	}
	raw, err := json.Marshal([]string(l))
	return string(raw), err
}

func (l *StringList) Scan(src any) error { return scanJSON(src, l) }

func (Case) TableName() string { return "moderation_cases" }
