package database

import (
	"path/filepath"
	"testing"
	"time"

	"knowforge/server/internal/config"
)

// SQLite 并发写事务：先读后写的两个事务交错时（A 读、B 读、A 写并提交、B 写），默认 deferred 事务的 B
// 无法升级为写锁而立即失败（busy_timeout 对此无效）；以 _txlock=immediate 打开后 B 在开始事务时排队，两个事务都成功。
func TestSQLiteConcurrentWriteTransactions(t *testing.T) {
	db, err := Open(config.DatabaseConfig{Type: TypeSQLite, Path: filepath.Join(t.TempDir(), "tx.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE counters (id INTEGER PRIMARY KEY, n INTEGER)").Error; err != nil {
		t.Fatal(err)
	}
	db.Exec("INSERT INTO counters (id, n) VALUES (1, 0)")

	txA := db.Begin()
	var n int
	txA.Raw("SELECT n FROM counters WHERE id = 1").Scan(&n)

	committed := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		txB := db.Begin() // immediate：等 A 提交后才开始
		if txB.Error != nil {
			result <- txB.Error
			return
		}
		var m int
		txB.Raw("SELECT n FROM counters WHERE id = 1").Scan(&m)
		<-committed
		if err := txB.Exec("UPDATE counters SET n = n + 1 WHERE id = 1").Error; err != nil {
			txB.Rollback()
			result <- err
			return
		}
		result <- txB.Commit().Error
	}()

	time.Sleep(100 * time.Millisecond) // 让 B 先开始（deferred 时 B 此时已读到快照）
	if err := txA.Exec("UPDATE counters SET n = n + 1 WHERE id = 1").Error; err != nil {
		t.Fatal(err)
	}
	if err := txA.Commit().Error; err != nil {
		t.Fatal(err)
	}
	close(committed)
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("交错的第二个写事务不应失败: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("第二个写事务未完成")
	}
	db.Raw("SELECT n FROM counters WHERE id = 1").Scan(&n)
	if n != 2 {
		t.Fatalf("两个事务都应生效，实际 %d", n)
	}
}
