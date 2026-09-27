// Package taskrun 进行中的后台任务登记（问答、写作助手、整本翻译等在后台 goroutine 中运行的 AI 任务）。
//
// 任务在哪个实例上运行就登记在哪个实例的内存中，同时由调用方把执行实例（cluster.Self()）写入任务记录。多实例部署时：
//   - Cancel：本实例在运行则直接取消，否则通知执行实例取消；
//   - RequestPartial：连在其他实例上的页面请求执行实例把已生成的部分结果经事件推送重新发一次（快照据此补齐）；
//   - Orphaned：任务既不在本实例运行、执行实例也已下线时，才视为中断（服务重启等），避免误判其他实例上的任务。
package taskrun

import (
	"strconv"
	"sync"

	"knowforge/server/internal/cluster"
)

// Runner 一个进行中的任务。
type Runner interface {
	// Cancel 取消任务。
	Cancel()
	// PublishPartial 经事件推送发出已生成的完整部分结果（供其他实例上的订阅者同步）。
	PublishPartial()
}

// Registry 某类任务的登记表；name 在全站唯一（跨实例频道名）。
type Registry struct {
	name  string
	tasks sync.Map // 任务 ID → Runner
}

// New 创建登记表并订阅其他实例发来的取消与同步请求。
func New(name string) *Registry {
	r := &Registry{name: name}
	cluster.Subscribe(name+".cancel", func(p []byte) {
		if id, err := strconv.ParseUint(string(p), 10, 64); err == nil {
			r.cancelLocal(uint(id))
		}
	})
	cluster.Subscribe(name+".partial", func(p []byte) {
		if id, err := strconv.ParseUint(string(p), 10, 64); err == nil {
			if run, ok := r.Load(uint(id)); ok {
				run.PublishPartial()
			}
		}
	})
	return r
}

// Add 登记任务；已登记时返回 false（不覆盖）。
func (r *Registry) Add(id uint, run Runner) bool {
	_, loaded := r.tasks.LoadOrStore(id, run)
	return !loaded
}

// Remove 任务结束后移除。
func (r *Registry) Remove(id uint) { r.tasks.Delete(id) }

// Load 本实例上进行中的任务。
func (r *Registry) Load(id uint) (Runner, bool) {
	v, ok := r.tasks.Load(id)
	if !ok {
		return nil, false
	}
	return v.(Runner), true
}

// Local 任务是否在本实例上运行。
func (r *Registry) Local(id uint) bool {
	_, ok := r.tasks.Load(id)
	return ok
}

func (r *Registry) cancelLocal(id uint) bool {
	if run, ok := r.Load(id); ok {
		run.Cancel()
		return true
	}
	return false
}

// Cancel 取消任务：本实例在运行则直接取消；否则执行实例在线时通知它取消。返回是否找到正在运行的任务。
func (r *Registry) Cancel(id uint, runner string) bool {
	if r.cancelLocal(id) {
		return true
	}
	if runner != "" && runner != cluster.Self() && cluster.Alive(runner) {
		cluster.Broadcast(r.name+".cancel", []byte(strconv.FormatUint(uint64(id), 10)))
		return true
	}
	return false
}

// RequestPartial 请求执行实例重新推送已生成的部分结果（任务不在本实例上运行时调用）。
func (r *Registry) RequestPartial(id uint) {
	cluster.Broadcast(r.name+".partial", []byte(strconv.FormatUint(uint64(id), 10)))
}

// Orphaned 任务是否已中断：不在本实例运行，且执行实例就是本实例（已结束登记）或已下线（空表示未知）。
func (r *Registry) Orphaned(id uint, runner string) bool {
	if r.Local(id) {
		return false
	}
	return runner == cluster.Self() || !cluster.Alive(runner)
}
