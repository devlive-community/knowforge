package aiwriter

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/ai"
	"knowforge/server/internal/eventhub"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

const (
	maxInputRunes       = 50000
	maxInstructionRunes = 500
	streamHeartbeat     = 25 * time.Second
)

type behavior struct{ core plugincore.Core }

func (b *behavior) Key() string { return plugins.KeyAIWriter }

func (b *behavior) RegisterRoutes(api *gin.RouterGroup, core plugincore.Core) {
	b.core = core
	feat := core.RequireFeaturePlugin(plugins.KeyAIWriter)
	use := core.RequirePermissionMiddleware(PermUse)
	with := func(h gin.HandlerFunc) []gin.HandlerFunc {
		return []gin.HandlerFunc{core.RequireAuth(), feat, use, h}
	}
	api.GET("/ai-writer/status", with(b.Status)...)
	api.POST("/ai-writer/tasks", with(b.CreateTask)...)
	api.GET("/ai-writer/tasks", with(b.ListTasks)...)
	api.GET("/ai-writer/tasks/:id", with(b.GetTask)...)
	api.GET("/ai-writer/tasks/:id/stream", core.RequireAuthStream(), feat, use, b.StreamTask)
	api.POST("/ai-writer/tasks/:id/cancel", with(b.CancelTask)...)
	api.POST("/ai-writer/tasks/:id/adopt", with(b.AdoptTask)...)
	api.DELETE("/ai-writer/tasks/:id", with(b.DeleteTask)...)
}

type quota struct {
	Limit int64 `json:"limit"` // -1 不限
	Used  int64 `json:"used"`
}

func monthStart(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
}

// quota 本月使用次数：进行中、已完成，以及取消/失败前已生成内容的任务都计一次。
func (b *behavior) quota(u *models.User) quota {
	q := quota{Limit: plugincore.EntitlementValue(b.core, u, entMonthlyUses)}
	b.core.Gorm().Model(&Task{}).
		Where("user_id = ? AND created_at >= ? AND (status IN ? OR result <> '')", u.ID, monthStart(time.Now()), []string{statusRunning, statusDone}).
		Count(&q.Used)
	return q
}

// Status GET /ai-writer/status AI 服务是否可用与本月额度。
func (b *behavior) Status(c *gin.Context) {
	chat, _ := b.core.AIStatus()
	b.core.OK(c, gin.H{"available": chat, "quota": b.quota(b.core.CurrentUser(c))})
}

// editableBook 按 ID 读取当前用户可编辑内容的书籍。
func (b *behavior) editableBook(c *gin.Context, u *models.User, id uint) (*models.Book, bool) {
	var book models.Book
	if id == 0 || b.core.Gorm().First(&book, id).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "书籍不存在")
		return nil, false
	}
	if !b.core.CanEditBookContent(u, &book) {
		b.core.Fail(c, http.StatusForbidden, "没有编辑这本书的权限")
		return nil, false
	}
	return &book, true
}

// CreateTask POST /ai-writer/tasks {book_id, doc_id?, action, text, before?, after?, instruction?}
// text 为处理对象（选中的文字；续写时为光标前的上文；大纲/摘要未选中时为整章内容），before/after 为紧邻的上下文。
// 校验与额度判定后立即返回 status=running 的任务，结果在后台生成；订阅 GET /ai-writer/tasks/:id/stream 获取实时文本。
func (b *behavior) CreateTask(c *gin.Context) {
	var req struct {
		BookID      uint   `json:"book_id"`
		DocID       uint   `json:"doc_id"`
		Action      string `json:"action"`
		Text        string `json:"text"`
		Before      string `json:"before"`
		After       string `json:"after"`
		Instruction string `json:"instruction"`
	}
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if _, ok := actionPrompts[req.Action]; !ok {
		b.core.Fail(c, http.StatusBadRequest, "不支持的操作")
		return
	}
	instruction := strings.TrimSpace(req.Instruction)
	if utf8.RuneCountInString(instruction) > maxInstructionRunes {
		b.core.Fail(c, http.StatusBadRequest, "附加要求不能超过 500 字")
		return
	}
	if req.Action == actionCustom && instruction == "" {
		b.core.Fail(c, http.StatusBadRequest, "请填写要求")
		return
	}
	input := req.Text
	if req.Action == actionContinue {
		input = tailRunes(req.Text, continueInputRunes)
	}
	if strings.TrimSpace(input) == "" {
		msg := "请先选中要处理的文字"
		if req.Action == actionContinue {
			msg = "光标前还没有内容，先写一点再续写"
		}
		b.core.Fail(c, http.StatusBadRequest, msg)
		return
	}
	if utf8.RuneCountInString(input) > maxInputRunes {
		b.core.Fail(c, http.StatusBadRequest, "选中的内容过长（超过 5 万字），请分段处理")
		return
	}
	u := b.core.CurrentUser(c)
	book, ok := b.editableBook(c, u, req.BookID)
	if !ok {
		return
	}
	docTitle := ""
	if req.DocID != 0 {
		var doc models.Document
		if b.core.Gorm().Where("id = ? AND book_id = ?", req.DocID, book.ID).First(&doc).Error != nil {
			b.core.Fail(c, http.StatusNotFound, "章节不存在")
			return
		}
		docTitle = doc.Title
	}
	if chat, _ := b.core.AIStatus(); !chat {
		b.core.Fail(c, http.StatusServiceUnavailable, "AI 服务暂未配置")
		return
	}
	if q := b.quota(u); !plugincore.WithinLimit(q.Limit, q.Used) {
		msg := "本月写作助手次数已用完，下月恢复，或提升等级/开通会员获得更多次数"
		if q.Limit == 0 {
			msg = "当前等级/会员不含 AI 写作助手，提升等级或开通会员后可用"
		}
		b.core.Fail(c, http.StatusTooManyRequests, msg)
		return
	}
	caller := ai.Caller{UserID: u.ID, Feature: "aiwriter." + req.Action, RefType: "book", RefID: book.ID, TraceID: ai.NewTraceID()}
	if req.DocID != 0 {
		caller.RefType, caller.RefID = "document", req.DocID
	}
	if err := b.core.AICheckQuota(ai.WithCaller(c.Request.Context(), caller)); err != nil {
		b.core.Fail(c, http.StatusTooManyRequests, err.Error())
		return
	}
	t := Task{UserID: u.ID, BookID: book.ID, DocID: req.DocID, Action: req.Action, Instruction: instruction, Input: input,
		Status: statusRunning, TraceID: caller.TraceID}
	if err := b.core.Gorm().Create(&t).Error; err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "创建任务失败")
		return
	}
	b.start(t, caller, buildPrompt(t, book.Title, docTitle, req.Before, req.After))
	b.core.OK(c, t)
}

// ListTasks GET /ai-writer/tasks?book_id=&doc_id=&page=&page_size= 我的写作助手记录（新→旧）。
func (b *behavior) ListTasks(c *gin.Context) {
	page, pageSize := b.core.Paginate(c)
	q := b.core.Gorm().Model(&Task{}).Where("user_id = ?", b.core.CurrentUser(c).ID)
	if id := b.core.AtoiDefault(c.Query("book_id"), 0); id > 0 {
		q = q.Where("book_id = ?", id)
	}
	if id := b.core.AtoiDefault(c.Query("doc_id"), 0); id > 0 {
		q = q.Where("doc_id = ?", id)
	}
	var total int64
	q.Count(&total)
	items := []Task{}
	q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items)
	b.core.OK(c, plugincore.PageResult{Items: items, Total: total, Page: page, PageSize: pageSize})
}

func (b *behavior) myTask(c *gin.Context) (Task, bool) {
	var t Task
	if b.core.Gorm().Where("id = ? AND user_id = ?", c.Param("id"), b.core.CurrentUser(c).ID).First(&t).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "记录不存在")
		return t, false
	}
	return t, true
}

// GetTask GET /ai-writer/tasks/:id
func (b *behavior) GetTask(c *gin.Context) {
	if t, ok := b.myTask(c); ok {
		b.core.OK(c, t)
	}
}

// CancelTask POST /ai-writer/tasks/:id/cancel 取消进行中的任务（已生成的文本保留，已产生的调用照常记录）。
func (b *behavior) CancelTask(c *gin.Context) {
	t, ok := b.myTask(c)
	if !ok {
		return
	}
	if t.Status != statusRunning || !cancelTask(t) {
		b.core.Fail(c, http.StatusConflict, "该任务已结束")
		return
	}
	b.core.OK(c, gin.H{"canceled": true})
}

// AdoptTask POST /ai-writer/tasks/:id/adopt {mode: replace|insert} 记录作者采纳了结果（内容由写作台写入正文）。
func (b *behavior) AdoptTask(c *gin.Context) {
	t, ok := b.myTask(c)
	if !ok {
		return
	}
	var req struct {
		Mode string `json:"mode"`
	}
	if c.ShouldBindJSON(&req) != nil || (req.Mode != "replace" && req.Mode != "insert") {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if strings.TrimSpace(t.Result) == "" || t.Status == statusRunning {
		b.core.Fail(c, http.StatusConflict, "还没有可采纳的结果")
		return
	}
	now := time.Now()
	b.core.Gorm().Model(&t).Updates(map[string]any{"adopted": req.Mode, "adopted_at": &now})
	t.Adopted, t.AdoptedAt = req.Mode, &now
	b.core.OK(c, t)
}

// DeleteTask DELETE /ai-writer/tasks/:id 删除一条记录（进行中的需先取消；AI 用量记录不受影响）。
func (b *behavior) DeleteTask(c *gin.Context) {
	t, ok := b.myTask(c)
	if !ok {
		return
	}
	if t.Status == statusRunning {
		b.core.Fail(c, http.StatusConflict, "任务进行中，请先取消")
		return
	}
	b.core.Gorm().Delete(&t)
	b.core.OK(c, gin.H{"deleted": true})
}

// taskView 流式快照：进行中时 result 为已生成的部分，result_seq 为其包含的片段序号（后续 delta 按序号去重）。
type taskView struct {
	Task
	ResultSeq int `json:"result_seq,omitempty"`
	// ResultSync 进行中且在其他实例上生成：快照不含已生成文本，随后推送 partial 补齐（此前的 delta 应忽略）
	ResultSync bool `json:"result_sync,omitempty"`
}

// StreamTask GET /ai-writer/tasks/:id/stream（?ticket= 事件流凭证鉴权）一个任务的实时生成：
// 先推 snapshot，之后逐段推 delta {seq, text}，结束推 done（最终记录）。
func (b *behavior) StreamTask(c *gin.Context) {
	userID := b.core.CurrentUser(c).ID
	load := func() (Task, bool) {
		var t Task
		err := b.core.Gorm().Where("id = ? AND user_id = ?", c.Param("id"), userID).First(&t).Error
		return t, err == nil
	}
	t, found := load()
	if !found {
		b.core.Fail(c, http.StatusNotFound, "记录不存在")
		return
	}
	eventhub.StartSSE(c)
	// 先订阅再读快照：快照之后的片段都会收到（快照已包含的由客户端按 seq 去重）
	ch := hub.Subscribe(t.ID)
	defer hub.Unsubscribe(t.ID, ch)
	t, _ = load()
	view := taskView{Task: t}
	remote := false
	if v, ok := running.Load(t.ID); ok && t.Status == statusRunning {
		view.Result, view.ResultSeq = v.(*runState).snapshot()
	} else if t.Status == statusRunning {
		view.ResultSync, remote = true, true // 在其他实例上生成：请求其推送 partial
	}
	snapshot, _ := json.Marshal(view)
	eventhub.Write(c.Writer, "snapshot", snapshot)
	if remote {
		running.RequestPartial(t.ID)
	}
	if t.Status != statusRunning {
		eventhub.Write(c.Writer, "done", snapshot)
		return
	}
	heartbeat := time.NewTicker(streamHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok { // 消费过慢被断开：结束本次连接，客户端重连后重新同步
				return
			}
			eventhub.Write(c.Writer, ev.Name, ev.Data)
			if ev.Name == "done" {
				return
			}
		case <-heartbeat.C:
			// 兜底：已结束但未收到推送（如服务重启后被巡检标记中断）
			if latest, ok := load(); ok && latest.Status != statusRunning {
				final, _ := json.Marshal(latest)
				eventhub.Write(c.Writer, "done", final)
				return
			}
			eventhub.Ping(c.Writer)
		}
	}
}
