package aiwriter

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"knowforge/server/internal/ai"
	"knowforge/server/internal/cluster"
	"knowforge/server/internal/eventhub"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/taskrun"
)

// 任务在后台生成：不受单个 HTTP 请求（及反向代理）超时约束，也不限制时长；生成的文本片段经 SSE 推给写作台（见 stream.go），
// 作者可随时取消（已生成的部分保留）。进程内登记进行中的任务，服务重启后遗留的进行中记录由巡检标记为中断。

var (
	running = taskrun.New("aiwriter.tasks") // 任务 ID → *runState
	hub     = eventhub.New("aiwriter.tasks", 1024)
)

// runState 进行中任务的内存状态：取消函数与已生成的文本（快照据此给中途连接的写作台）；seq 为片段序号，客户端据此去重。
type runState struct {
	id      uint
	cancel  context.CancelFunc
	mu      sync.Mutex
	partial strings.Builder
	seq     int
}

type deltaEvent struct {
	Seq  int    `json:"seq"`
	Text string `json:"text"`
}

// append 追加一个文本片段并推送 delta（在锁内推送，保证与快照一致）。
func (s *runState) append(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	s.partial.WriteString(text)
	hub.Publish(s.id, "delta", deltaEvent{Seq: s.seq, Text: text})
}

// Cancel 取消任务（taskrun.Runner）。
func (s *runState) Cancel() { s.cancel() }

// PublishPartial 推送已生成的完整文本（在锁内推送，与 delta 保持顺序），供其他实例上的订阅者同步。
func (s *runState) PublishPartial() {
	s.mu.Lock()
	defer s.mu.Unlock()
	hub.Publish(s.id, "partial", deltaEvent{Seq: s.seq, Text: s.partial.String()})
}

// snapshot 已生成的文本与序号。
func (s *runState) snapshot() (string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.partial.String(), s.seq
}

// cancelTask 取消进行中的任务（可能在其他实例上运行），返回是否找到。
func cancelTask(t Task) bool { return running.Cancel(t.ID, t.Runner) }

// 动作。
const (
	actionContinue = "continue"
	actionRewrite  = "rewrite"
	actionPolish   = "polish"
	actionExpand   = "expand"
	actionShorten  = "shorten"
	actionOutline  = "outline"
	actionSummary  = "summary"
	actionCustom   = "custom"
)

// actionPrompts 各动作的要求（custom 由作者填写）。
var actionPrompts = map[string]string{
	actionContinue: "从「上文」末尾自然地接着写下去（一到三段），衔接上文的语气、风格与内容，不要重复或复述上文。",
	actionRewrite:  "改写「待处理文字」：换一种表达方式，意思不变，篇幅相近。",
	actionPolish:   "润色「待处理文字」：修正错别字、语法与不通顺之处，让表达更准确流畅；不改变原意、观点与段落结构。",
	actionExpand:   "扩写「待处理文字」：补充细节、例子或解释，使内容更充实具体；保持原有观点与结构。",
	actionShorten:  "精简「待处理文字」：保留关键信息，删去冗余，篇幅约为原来的一半。",
	actionOutline:  "为本章生成结构清晰的大纲（Markdown 多级列表，两到三级）：覆盖「待处理文字」中已有的内容，并补充合理的后续结构。",
	actionSummary:  "为「待处理文字」写一段简明的摘要（一段话，约 100 到 200 字），概括核心内容与观点。",
	actionCustom:   "",
}

// 上下文窗口：只取紧邻处理对象的部分上下文，让模型把握语气与衔接（处理对象本身不截断）。
const (
	contextBeforeRunes = 3000
	contextAfterRunes  = 1000
	continueInputRunes = 6000 // 续写时的上文取光标前的这些字
)

const systemPrompt = "你是专业的写作助手，协助作者编辑书籍章节（Markdown）。" +
	"只输出处理后的正文本身，不要任何解释、前言或结束语，也不要用代码块包裹整个结果。" +
	"保持原文的语言、人称与 Markdown 格式（标题、列表、代码块、链接、图片等原样保留）。"

func tailRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

func headRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// buildPrompt 组装本次任务的用户消息。
func buildPrompt(t Task, bookTitle, docTitle, before, after string) string {
	var b strings.Builder
	b.WriteString("书名：" + bookTitle + "\n")
	if docTitle != "" {
		b.WriteString("章节：" + docTitle + "\n")
	}
	b.WriteString("\n要求：")
	if t.Action == actionCustom {
		b.WriteString("按作者的要求处理「待处理文字」：" + t.Instruction)
	} else {
		b.WriteString(actionPrompts[t.Action])
		if t.Instruction != "" {
			b.WriteString("\n作者的附加要求：" + t.Instruction)
		}
	}
	if t.Action == actionContinue {
		b.WriteString("\n\n<上文>\n" + t.Input + "\n</上文>")
		if after = strings.TrimSpace(headRunes(after, contextAfterRunes)); after != "" {
			b.WriteString("\n\n<下文（续写内容需要与之衔接，不要输出下文）>\n" + after + "\n</下文>")
		}
		return b.String()
	}
	if before = strings.TrimSpace(tailRunes(before, contextBeforeRunes)); before != "" {
		b.WriteString("\n\n<前文（仅供参考，不要输出）>\n" + before + "\n</前文>")
	}
	b.WriteString("\n\n<待处理文字>\n" + t.Input + "\n</待处理文字>")
	if after = strings.TrimSpace(headRunes(after, contextAfterRunes)); after != "" {
		b.WriteString("\n\n<后文（仅供参考，不要输出）>\n" + after + "\n</后文>")
	}
	return b.String()
}

// start 在后台生成（调用方标注随 ctx 传递，用于 AI 用量记录与每月额度）。
func (b *behavior) start(t Task, caller ai.Caller, prompt string) {
	ctx, cancel := context.WithCancel(ai.WithCaller(context.Background(), caller))
	st := &runState{id: t.ID, cancel: cancel}
	running.Add(t.ID, st)
	b.core.Gorm().Model(&Task{}).Where("id = ?", t.ID).Update("runner", cluster.Self())
	b.publishUserTask(t.ID)
	go func() {
		started := time.Now()
		defer func() {
			if r := recover(); r != nil {
				text, _ := st.snapshot()
				b.finish(t.ID, started, statusFailed, text, ai.ChatResponse{}, "生成出错，请重试")
			}
			running.Remove(t.ID)
			cancel()
		}()
		resp, err := b.core.AIChatStream(ctx, ai.ChatRequest{System: systemPrompt, Messages: []ai.Message{{Role: "user", Content: prompt}}}, st.append)
		text, _ := st.snapshot()
		switch {
		case errors.Is(err, context.Canceled):
			b.finish(t.ID, started, statusCanceled, text, resp, "已取消")
		case err != nil:
			b.finish(t.ID, started, statusFailed, text, resp, userError(err))
		default:
			if strings.TrimSpace(resp.Content) != "" {
				text = resp.Content
			}
			b.finish(t.ID, started, statusDone, strings.TrimSpace(text), resp, "")
		}
	}()
}

func userError(err error) string {
	if errors.Is(err, ai.ErrQuotaExceeded) {
		return err.Error()
	}
	return "AI 服务暂时不可用，请稍后再试"
}

func (b *behavior) finish(id uint, started time.Time, status, result string, resp ai.ChatResponse, errMsg string) {
	db := b.core.Gorm()
	db.Model(&Task{}).Where("id = ?", id).Updates(map[string]any{
		"status": status, "result": result, "error": errMsg, "model": resp.Model,
		"input_tokens": resp.Usage.InputTokens, "output_tokens": resp.Usage.OutputTokens, "estimated": resp.Usage.Estimated,
		"duration_ms": time.Since(started).Milliseconds(),
	})
	var final Task
	if db.First(&final, id).Error == nil {
		hub.Publish(id, "done", final)
		plugincore.PublishUserTask(b.core, final.UserID, writerUserTask(b.core, &final))
	}
}

// sweepInterrupted 把执行实例已下线的「进行中」任务标记为中断（服务重启等；其他在线实例上运行的不受影响）；
// 只处理创建超过 30 秒的记录，避开刚创建、尚未登记的任务。
func sweepInterrupted(core plugincore.Core) {
	db := core.Gorm()
	if !db.Migrator().HasTable(&Task{}) {
		return
	}
	var rows []Task
	db.Select("id, runner").Where("status = ? AND created_at < ?", statusRunning, time.Now().Add(-30*time.Second)).Find(&rows)
	for _, r := range rows {
		if running.Orphaned(r.ID, r.Runner) {
			db.Model(&Task{}).Where("id = ? AND status = ?", r.ID, statusRunning).
				Updates(map[string]any{"status": statusFailed, "error": "服务重启，生成已中断，请重试"})
			var task Task
			if db.First(&task, r.ID).Error == nil {
				plugincore.PublishUserTask(core, task.UserID, writerUserTask(core, &task))
			}
		}
	}
}
