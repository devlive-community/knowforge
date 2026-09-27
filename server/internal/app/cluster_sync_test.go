package app

import (
	"encoding/json"
	"testing"
	"time"

	"knowforge/server/internal/cluster"
)

// 其他实例发来的站内通知与插件日志送达本实例的订阅者。
func TestClusterSyncDelivers(t *testing.T) {
	a := &App{Notifications: newNotificationHub(), plugins: newPluginManager()}
	clusterApp.Store(a)
	defer clusterApp.Store(nil)

	ch := a.Notifications.subscribe(3)
	defer a.Notifications.unsubscribe(3, ch)
	raw, _ := json.Marshal(remoteNotification{UserID: 3, Message: `{"notification":{"id":1}}`})
	cluster.Inject(channelNotifications, raw)
	select {
	case msg := <-ch:
		if msg != `{"notification":{"id":1}}` {
			t.Fatalf("通知内容异常: %s", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("未收到其他实例的通知")
	}

	_, logs := a.plugins.subscribe("pdf-export")
	defer a.plugins.unsubscribe("pdf-export", logs)
	raw, _ = json.Marshal(remotePluginLog{Key: "pdf-export", Line: pluginLogLine{Level: "info", Text: "下载中"}})
	cluster.Inject(channelPluginLog, raw)
	select {
	case line := <-logs:
		if line.Text != "下载中" {
			t.Fatalf("日志内容异常: %+v", line)
		}
	case <-time.After(time.Second):
		t.Fatal("未收到其他实例的插件日志")
	}
	if history, _ := a.plugins.subscribe("pdf-export"); len(history) != 1 {
		t.Fatalf("其他实例的日志应进入历史缓冲: %v", history)
	}
}
