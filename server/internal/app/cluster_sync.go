package app

import (
	"encoding/json"
	"sync/atomic"

	"knowforge/server/internal/cluster"
)

// 多实例：本实例上的状态变化经 cluster 通知其他实例。
//   - 站内通知的实时推送：订阅者可能连在其他实例上；
//   - 插件安装/卸载日志：管理员的日志流可能连在其他实例上；
//   - 重新加载：插件启用/禁用后重算动态权限（内存中）并补齐本地文件，运行日志设置变化后重建日志写入器。

const (
	channelNotifications = "app.notifications"
	channelPluginLog     = "app.plugin_log"
	channelReload        = "app.reload"

	reloadPlugins = "plugins"
	reloadLogging = "logging"
)

// clusterApp 已登记到集群的应用实例（生产进程只有一个；未启动集群时为空）。
var clusterApp atomic.Pointer[App]

type remoteNotification struct {
	UserID  uint   `json:"user_id"`
	Message string `json:"message"`
}

type remotePluginLog struct {
	Key  string        `json:"key"`
	Line pluginLogLine `json:"line"`
}

func init() {
	cluster.Subscribe(channelNotifications, func(payload []byte) {
		var m remoteNotification
		if a := clusterApp.Load(); a != nil && json.Unmarshal(payload, &m) == nil {
			a.Notifications.deliver(m.UserID, m.Message)
		}
	})
	cluster.Subscribe(channelPluginLog, func(payload []byte) {
		var m remotePluginLog
		if a := clusterApp.Load(); a != nil && json.Unmarshal(payload, &m) == nil {
			a.plugins.append(m.Key, m.Line)
		}
	})
	cluster.Subscribe(channelReload, func(payload []byte) {
		a := clusterApp.Load()
		if a == nil {
			return
		}
		switch string(payload) {
		case reloadPlugins:
			a.syncPluginPermissions()
			a.syncLocalPluginFiles()
		case reloadLogging:
			a.initLogging()
		}
	})
}

// broadcastReload 通知其他实例重新加载某类状态。
func broadcastReload(what string) { cluster.Broadcast(channelReload, []byte(what)) }
