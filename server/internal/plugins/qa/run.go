package qa

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"knowforge/server/internal/ai"
	instances "knowforge/server/internal/cluster"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/taskrun"
)

// 问答在后台生成：不受单个 HTTP 请求（及反向代理）超时约束，也不限制时长与轮数；
// 每一步写入调用链并经 SSE 推送给正在查看的读者（见 stream.go）；读者可取消。进程内登记进行中的问答，服务重启后遗留的进行中记录由巡检标记为中断。

var runningAsks = taskrun.New("qa.asks") // 问答 ID → *runState

// runState 进行中问答的内存状态：取消函数与当前轮已生成的回答文本（逐字推送，快照据此给中途加入的读者）。
// seq 为全程递增的片段序号，客户端据此去重；某轮以工具调用结束时清空文本（reset），序号继续递增。
type runState struct {
	id      uint
	cancel  context.CancelFunc
	mu      sync.Mutex
	partial strings.Builder
	seq     int
}

type deltaEvent struct {
	Seq  int    `json:"seq"`
	Text string `json:"text,omitempty"`
}

// append 追加一个文本片段并推送 delta（在锁内推送，保证与快照一致）。
func (s *runState) append(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	s.partial.WriteString(text)
	asksHub.Publish(s.id, "delta", deltaEvent{Seq: s.seq, Text: text})
}

// reset 本轮以工具调用结束：清空临时文本并推送 reset。
func (s *runState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.partial.Len() == 0 {
		return
	}
	s.partial.Reset()
	asksHub.Publish(s.id, "reset", deltaEvent{Seq: s.seq})
}

// Cancel 取消问答（taskrun.Runner）。
func (s *runState) Cancel() { s.cancel() }

// PublishPartial 推送当前轮的完整部分回答（在锁内推送，与 delta 保持顺序），供其他实例上的订阅者同步。
func (s *runState) PublishPartial() {
	s.mu.Lock()
	defer s.mu.Unlock()
	asksHub.Publish(s.id, "partial", deltaEvent{Seq: s.seq, Text: s.partial.String()})
}

// snapshot 当前轮已生成的文本与序号。
func (s *runState) snapshot() (string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.partial.String(), s.seq
}

type runKey struct{}

func runFrom(ctx context.Context) *runState {
	st, _ := ctx.Value(runKey{}).(*runState)
	return st
}

// cancelAsk 取消进行中的问答（可能在其他实例上运行），返回是否找到。
func cancelAsk(rec Ask) bool { return runningAsks.Cancel(rec.ID, rec.Runner) }

// startAsk 在后台生成回答（调用方标注随 ctx 传递，用于 AI 用量记录与每月额度）。
func (b *behavior) startAsk(rec Ask, u models.User, book models.Book, caller ai.Caller, topK int) {
	ctx, cancel := context.WithCancel(ai.WithCaller(context.Background(), caller))
	st := &runState{id: rec.ID, cancel: cancel}
	ctx = context.WithValue(ctx, runKey{}, st)
	runningAsks.Add(rec.ID, st)
	b.core.Gorm().Model(&Ask{}).Where("id = ?", rec.ID).Update("runner", instances.Self())
	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.finishAsk(rec.ID, time.Now(), askFailed, answerResult{}, "回答生成出错，请重新提问")
			}
			runningAsks.Remove(rec.ID)
			cancel()
		}()
		b.runAsk(ctx, rec, &u, &book, topK)
	}()
}

func (b *behavior) runAsk(ctx context.Context, rec Ask, u *models.User, book *models.Book, topK int) {
	started := time.Now()
	db := b.core.Gorm()
	tr := newTracer(func(steps []TraceStep, t traceTotals) {
		elapsed := time.Since(started).Milliseconds()
		db.Model(&Ask{}).Where("id = ?", rec.ID).Updates(map[string]any{
			"trace": encodeTrace(steps), "calls": t.Calls, "input_tokens": t.InputTokens, "output_tokens": t.OutputTokens,
			"estimated": t.Estimated, "duration_ms": elapsed,
		})
		// 写库后再推送，保证订阅者读到的快照不会缺少已推送的步骤
		asksHub.Publish(rec.ID, "step", stepEvent{Index: len(steps) - 1, Step: steps[len(steps)-1], Calls: t.Calls,
			InputTokens: t.InputTokens, OutputTokens: t.OutputTokens, Estimated: t.Estimated, DurationMs: elapsed})
	})
	if _, err := b.ensureIndex(ctx, book.ID); err != nil {
		status := askFailed
		if errors.Is(err, context.Canceled) {
			status = askCanceled
		}
		b.finishAsk(rec.ID, started, status, answerResult{}, "建立索引失败，请稍后再试")
		return
	}
	chunks := b.loadChunks(u, book)
	var (
		res answerResult
		err error
	)
	if rec.Mode == "agent" {
		res, err = b.answerAgent(ctx, tr, book, chunks, rec.Question, rec.Selection, rec.DocID)
	} else {
		res, err = b.answerRAG(ctx, tr, book, chunks, rec.Question, rec.Selection, rec.DocID, topK)
	}
	if err != nil {
		status := askFailed
		if errors.Is(err, context.Canceled) {
			status = askCanceled
		}
		b.finishAsk(rec.ID, started, status, res, userError(err))
		return
	}
	b.finishAsk(rec.ID, started, askDone, res, "")
}

func (b *behavior) finishAsk(id uint, started time.Time, status string, res answerResult, errMsg string) {
	cites := res.Citations
	if cites == nil {
		cites = []Citation{}
	}
	raw, _ := json.Marshal(cites)
	db := b.core.Gorm()
	db.Model(&Ask{}).Where("id = ?", id).Updates(map[string]any{
		"status": status, "answer": res.Answer, "citations": string(raw), "steps": res.Steps, "error": errMsg,
		"duration_ms": time.Since(started).Milliseconds(),
	})
	var final Ask
	if db.First(&final, id).Error == nil {
		asksHub.Publish(id, "done", toAskView(final))
	}
}

const minTraceRetentionDays = 7

// purgeExpiredTraces 按保留天数清空过期问答的调用链明细（问答内容与消耗合计保留）。
func purgeExpiredTraces(core plugincore.Core) {
	days := core.AtoiDefault(core.GetSetting("qa_trace_retention_days"), 0)
	if days < minTraceRetentionDays {
		return
	}
	core.Gorm().Model(&Ask{}).Where("created_at < ? AND status <> ? AND trace <> ?", time.Now().AddDate(0, 0, -days), askRunning, "[]").
		Update("trace", "[]")
}

// sweepInterruptedAsks 把执行实例已下线的「进行中」问答标记为中断（服务重启等；其他在线实例上运行的不受影响）。
// 只处理创建超过 30 秒的记录，避开刚创建、尚未登记的问答。
func sweepInterruptedAsks(core plugincore.Core) {
	db := core.Gorm()
	if !db.Migrator().HasTable(&Ask{}) {
		return
	}
	var rows []Ask
	db.Select("id, runner").Where("status = ? AND created_at < ?", askRunning, time.Now().Add(-30*time.Second)).Find(&rows)
	for _, r := range rows {
		if runningAsks.Orphaned(r.ID, r.Runner) {
			db.Model(&Ask{}).Where("id = ? AND status = ?", r.ID, askRunning).
				Updates(map[string]any{"status": askFailed, "error": "服务重启，回答已中断，请重新提问"})
		}
	}
}
