package plugincore

import (
	"sort"
	"time"
)

// —— 「我的任务」：用户发起的后台任务（导入、AI 翻译、整站采集、写作助手等）统一汇总到一个页面。
// 核心与各插件登记任务来源（列出某用户的任务），并在任务状态或进度变化时经 PublishUserTask 推送，
// 核心把它经用户的通知事件流（SSE）实时送达，页面与导航栏的进行中标记据此更新，不轮询。——

// 任务状态（各来源把自己的状态映射到这几种）。
const (
	UserTaskQueued   = "queued"
	UserTaskRunning  = "running"
	UserTaskPaused   = "paused"
	UserTaskDone     = "done"
	UserTaskFailed   = "failed"
	UserTaskCanceled = "canceled"
)

// 「我的任务」页的分组：进行中（排队、执行、暂停待继续）/ 已完成 / 失败（含已取消）。
const (
	UserTaskTabActive = "active"
	UserTaskTabDone   = "done"
	UserTaskTabFailed = "failed"
)

// UserTaskTab 状态所属的分组。
func UserTaskTab(status string) string {
	switch status {
	case UserTaskDone:
		return UserTaskTabDone
	case UserTaskFailed, UserTaskCanceled:
		return UserTaskTabFailed
	}
	return UserTaskTabActive
}

// UserTask 一条任务的展示信息。Kind 为任务种类（前端据此显示种类文案与图标），ID 在种类内唯一；
// Total 为 0 表示进度不可计量（可用 Remaining 给出剩余数量）；Link 为站内的任务详情或结果页。
type UserTask struct {
	Kind       string     `json:"kind"`
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	Status     string     `json:"status"`
	Done       int        `json:"done"`
	Failed     int        `json:"failed"`
	Total      int        `json:"total"`
	Remaining  int        `json:"remaining"` // 进度不可计量（Total 为 0）时剩余待处理的数量，未知为 0
	Link       string     `json:"link"`
	Error      string     `json:"error"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// UserTaskSource 一种任务的来源。List 返回用户在分组 tab（UserTaskTab*）中的任务，按创建时间倒序，最多 limit 条；
// 插件未启用时核心不会调用。
type UserTaskSource struct {
	Kind   string
	Plugin string // 所属插件键（核心任务为空）；插件未启用时跳过
	List   func(core Core, userID uint, tab string, limit int) []UserTask
}

var userTaskSources []UserTaskSource

// RegisterUserTaskSource 登记任务来源（插件在 init 中调用）。
func RegisterUserTaskSource(s UserTaskSource) {
	userTaskSources = append(userTaskSources, s)
}

// UserTaskSources 已登记的任务来源（按种类排序）。
func UserTaskSources() []UserTaskSource {
	out := append([]UserTaskSource(nil), userTaskSources...)
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

// userTaskPublisher 由核心实现：把任务变化推送到用户的事件流。
type userTaskPublisher interface {
	PublishUserTask(userID uint, task UserTask)
}

// PublishUserTask 任务状态或进度变化时调用（失败静默，不影响任务本身）。
func PublishUserTask(core Core, userID uint, task UserTask) {
	if p, ok := core.(userTaskPublisher); ok && userID != 0 {
		p.PublishUserTask(userID, task)
	}
}
