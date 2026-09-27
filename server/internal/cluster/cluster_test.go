package cluster

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// 两个实例共享同一个数据库：发现对方、广播只发给其他实例、乱序提交的消息补拉、租约互斥与过期接管。
func TestTwoNodes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "c.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&Instance{}, &Event{}, &Lease{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	db.Create(&Instance{ID: "a", StartedAt: now, SeenAt: now})

	var mu sync.Mutex
	got := map[string][]string{}
	recv := func(node string) func(string, []byte) {
		return func(ch string, p []byte) {
			mu.Lock()
			got[node] = append(got[node], ch+":"+string(p))
			mu.Unlock()
		}
	}
	a := newNode(db, "a", recv("a"), func() {})
	if a.peers.Load() != 0 {
		t.Fatal("只有一个实例时不应有其他实例")
	}
	// 单实例时广播不写表
	a.broadcast("x", []byte("1"))
	var n int64
	db.Model(&Event{}).Count(&n)
	if n != 0 {
		t.Fatal("没有其他实例时不应写入消息")
	}

	db.Create(&Instance{ID: "b", StartedAt: now, SeenAt: now})
	b := newNode(db, "b", recv("b"), func() {})
	a.countPeers()
	if a.peers.Load() != 1 || b.peers.Load() != 1 {
		t.Fatalf("应发现对方: %d %d", a.peers.Load(), b.peers.Load())
	}

	a.broadcast("hub", []byte("hello"))
	b.broadcast("hub", []byte("world"))
	a.pull()
	b.pull()
	mu.Lock()
	if len(got["a"]) != 1 || got["a"][0] != "hub:world" || len(got["b"]) != 1 || got["b"][0] != "hub:hello" {
		t.Fatalf("广播应只送达其他实例: %v", got)
	}
	mu.Unlock()

	// 乱序提交：ID 12 先可见，ID 11 稍后提交，也应被补拉
	db.Create(&Event{ID: 12, Channel: "late", Origin: "a", Payload: "12", CreatedAt: time.Now()})
	b.pull()
	db.Create(&Event{ID: 11, Channel: "late", Origin: "a", Payload: "11", CreatedAt: time.Now()})
	b.pull()
	b.pull() // 已补拉到的不重复分发
	mu.Lock()
	late := 0
	for _, m := range got["b"] {
		if m == "late:11" || m == "late:12" {
			late++
		}
	}
	mu.Unlock()
	if late != 2 {
		t.Fatalf("乱序提交的消息应各分发一次: %v", got["b"])
	}

	// 租约：互斥、续期、过期后可被接管
	if !a.tryLease("sweep", time.Minute) || b.tryLease("sweep", time.Minute) {
		t.Fatal("租约应互斥")
	}
	if !a.tryLease("sweep", time.Minute) {
		t.Fatal("持有者应可续期")
	}
	db.Model(&Lease{}).Where("name = ?", "sweep").Update("expires_at", time.Now().Add(-time.Second))
	if !b.tryLease("sweep", time.Minute) {
		t.Fatal("过期租约应可被接管")
	}
}

// 未启动时：广播不做任何事、租约总是成功、只认本实例在线。
func TestWithoutStart(t *testing.T) {
	Broadcast("x", []byte("1"))
	if !TryLease("anything", time.Second) || !Alive(Self()) || Alive("other") || Peers() != 0 {
		t.Fatal("未启动时的默认行为异常")
	}
}

// Start/Stop：登记与注销本实例。
func TestStartStop(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "s.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Start(context.Background(), db, "test"); err != nil {
		t.Fatal(err)
	}
	if list := Instances(); len(list) != 1 || list[0].ID != Self() {
		t.Fatalf("应登记本实例: %v", list)
	}
	Stop()
	var n int64
	db.Model(&Instance{}).Count(&n)
	if n != 0 {
		t.Fatal("停止后应注销")
	}
}
