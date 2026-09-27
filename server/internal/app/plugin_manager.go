package app

import (
	"encoding/json"
	"sync"
	"time"

	"knowforge/server/internal/cluster"
)

const pluginLogBufferMax = 200

// pluginLogLine 一条插件操作日志（安装/卸载进度），经 SSE 推送给前端。
type pluginLogLine struct {
	Time  string `json:"time"`
	Level string `json:"level"` // info | success | error
	Text  string `json:"text"`
}

// pluginManager 跟踪每个插件的“操作代次”并广播操作日志。
//
// 代次（generation）用于避免竞态：安装在后台 goroutine 中进行，若期间管理员卸载/重装，
// 旧 goroutine 的 DB 写入必须被丢弃——否则 GORM Save（主键已存在时会 upsert）会把已删除的
// 插件行重新写回，导致“卸载后仍显示已安装”。
//
// 日志用于让前端实时看到安装/卸载进度（SSE）。
type pluginManager struct {
	mu   sync.Mutex
	gen  map[string]int64
	subs map[string]map[chan pluginLogLine]struct{}
	logs map[string][]pluginLogLine
}

func newPluginManager() *pluginManager {
	return &pluginManager{
		gen:  map[string]int64{},
		subs: map[string]map[chan pluginLogLine]struct{}{},
		logs: map[string][]pluginLogLine{},
	}
}

// begin 开启针对某插件的新操作：递增代次令牌并清空历史日志缓冲。
func (m *pluginManager) begin(key string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gen[key]++
	m.logs[key] = nil
	return m.gen[key]
}

// current 判断给定代次是否仍是该插件的当前操作。
func (m *pluginManager) current(key string, gen int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gen[key] == gen
}

// log 记录并广播一条日志（level: info|success|error），同时发给其他实例上的日志订阅者。
func (m *pluginManager) log(key, level, text string) {
	line := pluginLogLine{Time: time.Now().Format("15:04:05"), Level: level, Text: text}
	m.append(key, line)
	if raw, err := json.Marshal(remotePluginLog{Key: key, Line: line}); err == nil {
		cluster.Broadcast(channelPluginLog, raw)
	}
}

// append 记录一条日志并推送给本实例的订阅者。
func (m *pluginManager) append(key string, line pluginLogLine) {
	m.mu.Lock()
	buf := append(m.logs[key], line)
	if len(buf) > pluginLogBufferMax {
		buf = buf[len(buf)-pluginLogBufferMax:]
	}
	m.logs[key] = buf
	subs := make([]chan pluginLogLine, 0, len(m.subs[key]))
	for ch := range m.subs[key] {
		subs = append(subs, ch)
	}
	m.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- line:
		default: // 订阅者缓冲已满则丢弃该行，避免阻塞操作
		}
	}
}

// subscribe 订阅某插件的日志，返回历史缓冲快照与实时通道。
func (m *pluginManager) subscribe(key string) ([]pluginLogLine, chan pluginLogLine) {
	ch := make(chan pluginLogLine, 64)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.subs[key] == nil {
		m.subs[key] = map[chan pluginLogLine]struct{}{}
	}
	m.subs[key][ch] = struct{}{}
	history := append([]pluginLogLine(nil), m.logs[key]...)
	return history, ch
}

// unsubscribe 取消订阅。
func (m *pluginManager) unsubscribe(key string, ch chan pluginLogLine) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if subs := m.subs[key]; subs != nil {
		delete(subs, ch)
	}
}
