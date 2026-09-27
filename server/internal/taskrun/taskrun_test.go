package taskrun

import (
	"testing"

	"knowforge/server/internal/cluster"
)

type fakeRun struct{ canceled, published int }

func (f *fakeRun) Cancel()         { f.canceled++ }
func (f *fakeRun) PublishPartial() { f.published++ }

// 本实例登记、取消与中断判定；其他实例发来的取消与同步请求作用于本实例上的任务。
func TestRegistry(t *testing.T) {
	r := New("test.tasks")
	run := &fakeRun{}
	if !r.Add(5, run) || r.Add(5, &fakeRun{}) {
		t.Fatal("重复登记应被拒绝")
	}
	if !r.Cancel(5, cluster.Self()) || run.canceled != 1 {
		t.Fatal("本实例的任务应被直接取消")
	}
	if r.Orphaned(5, cluster.Self()) {
		t.Fatal("本实例上运行的任务不是中断")
	}
	// 其他实例发来的请求
	cluster.Inject("test.tasks.cancel", []byte("5"))
	cluster.Inject("test.tasks.partial", []byte("5"))
	cluster.Inject("test.tasks.partial", []byte("6")) // 不在本实例的忽略
	if run.canceled != 2 || run.published != 1 {
		t.Fatalf("应响应其他实例的请求: %+v", run)
	}
	r.Remove(5)
	if !r.Orphaned(5, cluster.Self()) || !r.Orphaned(5, "") || !r.Orphaned(5, "gone") {
		t.Fatal("不在本实例、执行实例不在线的任务应视为中断")
	}
	if r.Cancel(5, "gone") {
		t.Fatal("执行实例不在线时取消应返回未找到")
	}
}
