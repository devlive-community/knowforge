// Package eventhub 事件推送中心（SSE）：按对象 ID 订阅，非阻塞发布。
// 供后台运行的 AI 任务（问答、写作助手等）把进度推给正在查看的用户。
// 多实例部署时，发布的事件经 cluster 同时送达其他实例上的订阅者（订阅者可以连在任意实例上）。
// 订阅者消费过慢（缓冲已满）时被断开，EventSource 自动重连后由接口重新推送快照，不丢状态。
package eventhub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/cluster"
	"knowforge/server/internal/metrics"
)

// Event 一条待推送的事件（data 为 JSON）。
type Event struct {
	Name string
	Data []byte
}

// Hub 按对象 ID 分组的订阅者集合。
type Hub struct {
	channel string
	buffer  int
	mu      sync.Mutex
	subs    map[uint]map[chan Event]struct{}
}

// remoteEvent 跨实例传递的一条事件。
type remoteEvent struct {
	ID   uint            `json:"id"`
	Name string          `json:"name"`
	Data json.RawMessage `json:"data"`
}

// New 创建推送中心；name 在全站唯一（跨实例频道名），buffer 为每个订阅者的缓冲事件数。
func New(name string, buffer int) *Hub {
	h := &Hub{channel: "hub:" + name, buffer: buffer, subs: map[uint]map[chan Event]struct{}{}}
	cluster.Subscribe(h.channel, func(payload []byte) {
		var ev remoteEvent
		if json.Unmarshal(payload, &ev) == nil {
			h.deliver(ev.ID, Event{Name: ev.Name, Data: ev.Data})
		}
	})
	return h
}

// Subscribe 订阅对象 id 的事件。
func (h *Hub) Subscribe(id uint) chan Event {
	ch := make(chan Event, h.buffer)
	h.mu.Lock()
	if h.subs[id] == nil {
		h.subs[id] = map[chan Event]struct{}{}
	}
	h.subs[id][ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe 取消订阅（已被断开的订阅者忽略）。
func (h *Hub) Unsubscribe(id uint, ch chan Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[id][ch]; ok {
		delete(h.subs[id], ch)
		close(ch)
	}
	if len(h.subs[id]) == 0 {
		delete(h.subs, id)
	}
}

// Publish 非阻塞推送给本实例与其他实例上的订阅者；缓冲已满的订阅者被断开（其通道被关闭）。
func (h *Hub) Publish(id uint, name string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	h.deliver(id, Event{Name: name, Data: raw})
	if msg, err := json.Marshal(remoteEvent{ID: id, Name: name, Data: raw}); err == nil {
		cluster.Broadcast(h.channel, msg)
	}
}

// deliver 推送给本实例的订阅者。
func (h *Hub) deliver(id uint, ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[id] {
		select {
		case ch <- ev:
		default:
			delete(h.subs[id], ch)
			close(ch)
		}
	}
}

// StartSSE 写出事件流响应头（关闭代理缓冲）。
func StartSSE(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	// 运行指标：当前打开的事件流连接数（请求结束或客户端断开时减一）
	route := c.FullPath()
	sseConnections.Add(1, route)
	go func() {
		<-c.Request.Context().Done()
		sseConnections.Add(-1, route)
	}()
}

var sseConnections = metrics.Default.NewGaugeVec("knowforge_sse_connections", "Open server-sent event streams on this instance.", "route")

// Write 写出一条事件并立即刷新。
func Write(w gin.ResponseWriter, name string, data []byte) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
	w.Flush()
}

// Ping 写出心跳注释行。
func Ping(w gin.ResponseWriter) {
	fmt.Fprint(w, ": ping\n\n")
	w.Flush()
}
