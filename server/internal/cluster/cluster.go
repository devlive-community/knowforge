// Package cluster 多实例协作：实例登记（心跳）、跨实例消息与租约。
//
// 多个服务实例共享同一个数据库（PostgreSQL / MySQL）时：
//   - 每个实例启动时登记自己并定期心跳，其他实例据此判断某个实例是否仍在运行（如后台 AI 任务的归属）；
//   - Broadcast 把消息发给其他实例（经数据库表 cluster_events，各实例约每 100 毫秒拉取一次），
//     只有检测到其他在线实例时才写表，单实例部署没有额外开销；
//   - TryLease 基于数据库的租约，用于只需一个实例执行的工作（周期巡检）与跨实例互斥（如同一本书的索引重建）。
//
// 未调用 Start（测试、安装向导阶段）时：Broadcast 不做任何事，Alive 只认本实例，TryLease 总是成功。
package cluster

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	heartbeatEvery = 10 * time.Second
	aliveWithin    = 35 * time.Second // 超过此时间没有心跳视为已下线
	pollEvery      = 100 * time.Millisecond
	idlePollEvery  = 2 * time.Second // 没有其他实例时仍低频检查，以便及时发现新实例
	eventRetention = 2 * time.Minute
	batchSize      = 500
	gapWait        = 3 * time.Second // 自增 ID 可能乱序提交：跳过的 ID 在这段时间内继续补拉
	maxGap         = 1000            // 一次跳过的 ID 过多（如序列跳号）时不再补拉
)

// Instance 一个运行中的服务实例。
type Instance struct {
	ID        string    `gorm:"primaryKey;size:32" json:"id"`
	Host      string    `gorm:"size:255" json:"host"`
	PID       int       `json:"pid"`
	Version   string    `gorm:"size:40" json:"version"`
	StartedAt time.Time `json:"started_at"`
	SeenAt    time.Time `gorm:"index" json:"seen_at"`
}

func (Instance) TableName() string { return "cluster_instances" }

// Event 一条跨实例消息。
type Event struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement"`
	Channel   string    `gorm:"size:100"`
	Origin    string    `gorm:"size:32"`
	Payload   string    `gorm:"type:text"`
	CreatedAt time.Time `gorm:"index"`
}

func (Event) TableName() string { return "cluster_events" }

// Lease 一个租约：Holder 在 ExpiresAt 之前独占 Name。
type Lease struct {
	Name      string    `gorm:"primaryKey;size:120"`
	Holder    string    `gorm:"size:32"`
	ExpiresAt time.Time `gorm:"index"`
}

func (Lease) TableName() string { return "cluster_leases" }

var (
	self = newID()

	subsMu sync.RWMutex
	subs   = map[string][]func([]byte){}

	current atomic.Pointer[Node]
)

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Self 本实例 ID。
func Self() string { return self }

// Subscribe 订阅其他实例发来的某频道消息（可在 Start 之前调用，如插件 init）。本实例 Broadcast 的消息不会回送给自己。
func Subscribe(channel string, h func(payload []byte)) {
	subsMu.Lock()
	subs[channel] = append(subs[channel], h)
	subsMu.Unlock()
}

func dispatch(channel string, payload []byte) {
	subsMu.RLock()
	hs := subs[channel]
	subsMu.RUnlock()
	for _, h := range hs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[cluster] 处理消息 %s 出错: %v", channel, r)
				}
			}()
			h(payload)
		}()
	}
}

// Broadcast 把消息发给其他在线实例（没有其他实例或未启动时不做任何事）。
func Broadcast(channel string, payload []byte) {
	if n := current.Load(); n != nil {
		n.broadcast(channel, payload)
	}
}

func (n *Node) broadcast(channel string, payload []byte) {
	if n.peers.Load() == 0 {
		return
	}
	if err := n.db.Create(&Event{Channel: channel, Origin: n.id, Payload: string(payload), CreatedAt: time.Now()}).Error; err != nil {
		log.Printf("[cluster] 广播 %s 失败: %v", channel, err)
	}
}

// Peers 当前其他在线实例数。
func Peers() int {
	if n := current.Load(); n != nil {
		return int(n.peers.Load())
	}
	return 0
}

// Alive 实例是否在线（空 ID 视为不在线；未启动时只认本实例）。
func Alive(id string) bool {
	if id == "" {
		return false
	}
	if id == self {
		return true
	}
	n := current.Load()
	if n == nil {
		return false
	}
	var count int64
	n.db.Model(&Instance{}).Where("id = ? AND seen_at > ?", id, time.Now().Add(-aliveWithin)).Count(&count)
	return count > 0
}

// Instances 在线实例列表（管理后台展示）。
func Instances() []Instance {
	n := current.Load()
	if n == nil {
		return nil
	}
	var out []Instance
	n.db.Where("seen_at > ?", time.Now().Add(-aliveWithin)).Order("started_at").Find(&out)
	return out
}

// TryLease 尝试获得（或续期）租约 name，有效期 ttl；已由其他实例持有且未过期时返回 false。未启动时总是成功。
func TryLease(name string, ttl time.Duration) bool {
	n := current.Load()
	if n == nil {
		return true
	}
	return n.tryLease(name, ttl)
}

func (n *Node) tryLease(name string, ttl time.Duration) bool {
	now := time.Now()
	res := n.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&Lease{Name: name, Holder: n.id, ExpiresAt: now.Add(ttl)})
	if res.Error == nil && res.RowsAffected == 1 {
		return true
	}
	res = n.db.Model(&Lease{}).Where("name = ? AND (holder = ? OR expires_at < ?)", name, n.id, now).
		Updates(map[string]any{"holder": n.id, "expires_at": now.Add(ttl)})
	return res.Error == nil && res.RowsAffected == 1
}

// ReleaseLease 释放本实例持有的租约。
func ReleaseLease(name string) {
	if n := current.Load(); n != nil {
		n.db.Where("name = ? AND holder = ?", name, self).Delete(&Lease{})
	}
}

// Node 本实例在集群中的运行状态。
type Node struct {
	id       string
	db       *gorm.DB
	dispatch func(channel string, payload []byte)
	peers    atomic.Int64
	lastID   uint64
	gaps     map[uint64]time.Time // 尚未见到的较小 ID（其事务可能晚于更大的 ID 提交）
	stop     context.CancelFunc
	done     chan struct{}
}

// Start 登记本实例并开始心跳与拉取消息（重复调用时先停止之前的）；ctx 结束时注销。
func Start(ctx context.Context, db *gorm.DB, version string) error {
	if err := db.AutoMigrate(&Instance{}, &Event{}, &Lease{}); err != nil {
		return err
	}
	Stop()
	host, _ := os.Hostname()
	now := time.Now()
	if err := db.Save(&Instance{ID: self, Host: host, PID: os.Getpid(), Version: version, StartedAt: now, SeenAt: now}).Error; err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	n := newNode(db, self, dispatch, cancel)
	current.Store(n)
	go n.run(runCtx)
	return nil
}

func newNode(db *gorm.DB, id string, dispatch func(string, []byte), stop context.CancelFunc) *Node {
	n := &Node{id: id, db: db, dispatch: dispatch, gaps: map[uint64]time.Time{}, stop: stop, done: make(chan struct{})}
	var last Event
	if db.Order("id DESC").Limit(1).Find(&last).Error == nil {
		n.lastID = last.ID // 只接收启动之后的消息
	}
	n.countPeers()
	return n
}

// Stop 注销本实例并停止后台循环。
func Stop() {
	n := current.Swap(nil)
	if n == nil {
		return
	}
	n.stop()
	<-n.done
	n.db.Delete(&Instance{}, "id = ?", self)
	n.db.Where("holder = ?", self).Delete(&Lease{})
}

func (n *Node) countPeers() {
	var count int64
	n.db.Model(&Instance{}).Where("id <> ? AND seen_at > ?", n.id, time.Now().Add(-aliveWithin)).Count(&count)
	n.peers.Store(count)
}

func (n *Node) run(ctx context.Context) {
	defer close(n.done)
	heartbeat := time.NewTicker(heartbeatEvery)
	defer heartbeat.Stop()
	cleanup := time.NewTicker(time.Minute)
	defer cleanup.Stop()
	poll := time.NewTimer(pollEvery)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			n.db.Model(&Instance{}).Where("id = ?", n.id).Update("seen_at", time.Now())
			n.countPeers()
		case <-cleanup.C:
			n.db.Where("created_at < ?", time.Now().Add(-eventRetention)).Delete(&Event{})
			n.db.Where("seen_at < ?", time.Now().Add(-24*time.Hour)).Delete(&Instance{})
		case <-poll.C:
			n.pull()
			if n.peers.Load() > 0 {
				poll.Reset(pollEvery)
			} else {
				poll.Reset(idlePollEvery)
			}
		}
	}
}

// pull 拉取其他实例发来的新消息并分发（含补拉此前跳过的 ID）。
func (n *Node) pull() {
	now := time.Now()
	for id, since := range n.gaps {
		if now.Sub(since) > gapWait {
			delete(n.gaps, id)
		}
	}
	for {
		q := n.db.Where("id > ?", n.lastID)
		if len(n.gaps) > 0 {
			ids := make([]uint64, 0, len(n.gaps))
			for id := range n.gaps {
				ids = append(ids, id)
			}
			q = n.db.Where("id > ? OR id IN ?", n.lastID, ids)
		}
		var events []Event
		if q.Order("id").Limit(batchSize).Find(&events).Error != nil || len(events) == 0 {
			return
		}
		for _, ev := range events {
			if ev.ID <= n.lastID {
				delete(n.gaps, ev.ID) // 补拉到的
			} else {
				if n.lastID > 0 && ev.ID-n.lastID <= maxGap {
					for missing := n.lastID + 1; missing < ev.ID; missing++ {
						n.gaps[missing] = now
					}
				}
				n.lastID = ev.ID
			}
			if ev.Origin != n.id {
				n.dispatch(ev.Channel, []byte(ev.Payload))
			}
		}
		if len(events) < batchSize {
			return
		}
	}
}

// Inject 模拟收到其他实例发来的消息（用于测试跨实例行为）。
func Inject(channel string, payload []byte) { dispatch(channel, payload) }

// Lock 跨实例互斥：阻塞直到获得租约 name（或 ctx 结束），持有期间自动续期，返回释放函数。
// 同一实例内的互斥需调用方另加本地锁（租约以实例为持有者，同一实例重复获取会成功）。
func Lock(ctx context.Context, name string, ttl time.Duration) (func(), error) {
	for !TryLease(name, ttl) {
		select {
		case <-ctx.Done():
			return func() {}, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	renewCtx, stop := context.WithCancel(context.Background())
	go func() {
		t := time.NewTicker(ttl / 3)
		defer t.Stop()
		for {
			select {
			case <-renewCtx.Done():
				return
			case <-t.C:
				TryLease(name, ttl)
			}
		}
	}()
	return func() {
		stop()
		ReleaseLease(name)
	}, nil
}
