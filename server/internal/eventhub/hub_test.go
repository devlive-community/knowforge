package eventhub

import (
	"encoding/json"
	"testing"
	"time"

	"knowforge/server/internal/cluster"
)

// 本实例发布送达本实例订阅者；其他实例发来的事件送达本实例订阅者（按对象 ID 分组）。
func TestHubLocalAndRemote(t *testing.T) {
	h := New("test.hub", 4)
	ch := h.Subscribe(7)
	other := h.Subscribe(8)
	defer h.Unsubscribe(7, ch)
	defer h.Unsubscribe(8, other)

	h.Publish(7, "local", map[string]int{"n": 1})
	msg, _ := json.Marshal(remoteEvent{ID: 7, Name: "remote", Data: json.RawMessage(`{"n":2}`)})
	cluster.Inject("hub:test.hub", msg)

	for _, want := range []string{"local", "remote"} {
		select {
		case ev := <-ch:
			if ev.Name != want {
				t.Fatalf("事件顺序或名称异常: %s != %s", ev.Name, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("未收到 %s 事件", want)
		}
	}
	select {
	case ev := <-other:
		t.Fatalf("其他对象的订阅者不应收到: %v", ev)
	default:
	}
}
