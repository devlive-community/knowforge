package qa

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/eventhub"
)

// 问答进度推送（SSE）：后台生成的每一步都推给正在查看的读者，不需要前端轮询。
// GET /qa/asks/:id/stream 先推 snapshot（当前完整记录，进行中时含已生成的部分回答与 answer_seq），之后逐步推 step（新增的调用链步骤与合计）、
// delta（回答文本片段 {seq, text}）、reset（本轮以工具调用结束，清空临时文本 {seq}），结束推 done（最终记录）。
// 内存 hub 与站内通知推送相同，适用于单实例部署；订阅者消费过慢时断开连接，EventSource 自动重连后重新获得 snapshot，不丢步骤。

const (
	streamBuffer    = 1024
	streamHeartbeat = 25 * time.Second
)

var asksHub = eventhub.New("qa.asks", streamBuffer)

// stepEvent 一个新增步骤及调用链合计（index 为该步骤在 trace 中的下标）。
type stepEvent struct {
	Index        int       `json:"index"`
	Step         TraceStep `json:"step"`
	Calls        int       `json:"calls"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	Estimated    bool      `json:"estimated"`
	DurationMs   int64     `json:"duration_ms"`
}

// StreamAsk GET /qa/asks/:id/stream（?ticket= 事件流凭证鉴权）我的一条问答的实时进度。
func (b *behavior) StreamAsk(c *gin.Context) {
	userID := b.core.CurrentUser(c).ID
	load := func() (Ask, bool) {
		var rec Ask
		err := b.core.Gorm().Where("id = ? AND user_id = ?", c.Param("id"), userID).First(&rec).Error
		return rec, err == nil
	}
	rec, found := load()
	if !found {
		b.core.Fail(c, http.StatusNotFound, "记录不存在")
		return
	}
	eventhub.StartSSE(c)

	// 先订阅再读快照：快照之后的步骤都会收到（快照前已包含的步骤由客户端按 index 去重）
	ch := asksHub.Subscribe(rec.ID)
	defer asksHub.Unsubscribe(rec.ID, ch)
	rec, _ = load()
	view := toAskView(rec)
	remote := false
	if v, ok := runningAsks.Load(rec.ID); ok && rec.Status == askRunning {
		view.Answer, view.AnswerSeq = v.(*runState).snapshot() // 已生成的部分回答（后续 delta 按 seq 去重）
	} else if rec.Status == askRunning {
		view.AnswerSync, remote = true, true // 在其他实例上生成：请求其推送 partial
	}
	snapshot, _ := json.Marshal(view)
	eventhub.Write(c.Writer, "snapshot", snapshot)
	if remote {
		runningAsks.RequestPartial(rec.ID)
	}
	if rec.Status != askRunning {
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
			// 心跳，并兜底检查已结束但未收到推送的情况（如服务重启后被巡检标记中断）
			if latest, ok := load(); ok && latest.Status != askRunning {
				final, _ := json.Marshal(toAskView(latest))
				eventhub.Write(c.Writer, "done", final)
				return
			}
			eventhub.Ping(c.Writer)
		}
	}
}
