package app

import (
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/cluster"
	"knowforge/server/internal/config"
)

// 多实例状态：管理后台查看在线的服务实例，并检查多实例部署的前提（数据库、共享数据目录、版本一致）。
// 每个实例启动后在数据目录的 cluster/ 下写一个以实例 ID 命名的标记文件；看不到其他实例的标记文件，说明数据目录没有共享，
// 上传的文件、导出结果与插件文件只存在于各自的实例上。

func clusterMarkerDir() string { return filepath.Join(config.DataDir(), "cluster") }

// writeClusterMarker 写入本实例的标记文件，并清理已下线实例遗留的标记。
func writeClusterMarker() {
	dir := clusterMarkerDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, cluster.Self()), []byte(time.Now().Format(time.RFC3339)), 0o644)
	online := map[string]bool{}
	for _, in := range cluster.Instances() {
		online[in.ID] = true
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && !online[e.Name()] && time.Since(info.ModTime()) > 24*time.Hour {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func removeClusterMarker() { _ = os.Remove(filepath.Join(clusterMarkerDir(), cluster.Self())) }

type clusterInstanceView struct {
	cluster.Instance
	Self       bool `json:"self"`
	DataShared bool `json:"data_shared"` // 本实例能看到它的标记文件（数据目录共享）
}

// AdminClusterStatus GET /system/cluster（管理员） 在线实例与多实例部署检查。
// warnings：sqlite（多实例使用 SQLite）、data_dir（数据目录未共享）、version（各实例版本不一致）、upgrade（在线升级只升级当前实例）。
func (a *App) AdminClusterStatus(c *gin.Context) {
	list := cluster.Instances()
	items := make([]clusterInstanceView, 0, len(list))
	warnings := []string{}
	shared, sameVersion := true, true
	for _, in := range list {
		v := clusterInstanceView{Instance: in, Self: in.ID == cluster.Self(), DataShared: true}
		if !v.Self {
			if _, err := os.Stat(filepath.Join(clusterMarkerDir(), in.ID)); err != nil {
				v.DataShared, shared = false, false
			}
		}
		if in.Version != Version {
			sameVersion = false
		}
		items = append(items, v)
	}
	if len(items) > 1 {
		if a.Config.Database.Type == "sqlite" {
			warnings = append(warnings, "sqlite")
		}
		if !shared {
			warnings = append(warnings, "data_dir")
		}
		if !sameVersion {
			warnings = append(warnings, "version")
		}
		warnings = append(warnings, "upgrade")
	}
	ok(c, gin.H{"self": cluster.Self(), "instances": items, "database": a.Config.Database.Type, "warnings": warnings})
}
