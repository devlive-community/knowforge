package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// newInstalledApp 启动并安装一个临时 sqlite 应用，返回带 DB 的实例。
func newInstalledApp(t *testing.T) *App {
	t.Helper()
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("创建应用失败: %v", err)
	}
	ts := httptest.NewServer(a.Router())
	t.Cleanup(ts.Close)
	install := `{"database":` + testdb.InstallJSON(t) + `,"site":{"name":"t"},"admin":{"username":"admin","email":"a@b.c","password":"secret123"}}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/setup/install", strings.NewReader(install))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("安装失败: %v", err)
	}
	resp.Body.Close()
	if a.DB == nil {
		t.Fatalf("安装后 DB 仍为空")
	}
	return a
}

func TestDBRateLimitStoreWindowAndReset(t *testing.T) {
	a := newInstalledApp(t)
	store := newDBRateLimitStore(a.DB)
	now := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)

	// 额度 3：连续 3 次放行，remaining 递减。
	for expectedRemaining := 2; expectedRemaining >= 0; expectedRemaining-- {
		allowed, remaining, _ := store.Take("k1", 3, time.Minute, now)
		if !allowed || remaining != expectedRemaining {
			t.Fatalf("第 %d 次应放行且 remaining=%d，实际 allowed=%v remaining=%d", 3-expectedRemaining, expectedRemaining, allowed, remaining)
		}
	}
	// 第 4 次（窗口内）超限。
	allowed, remaining, retry := store.Take("k1", 3, time.Minute, now.Add(10*time.Second))
	if allowed || remaining != 0 || retry != 50*time.Second {
		t.Fatalf("超限结果异常: allowed=%v remaining=%d retry=%s", allowed, remaining, retry)
	}
	// 窗口到期后恢复额度。
	allowed, remaining, _ = store.Take("k1", 3, time.Minute, now.Add(time.Minute))
	if !allowed || remaining != 2 {
		t.Fatalf("窗口到期后应恢复额度: allowed=%v remaining=%d", allowed, remaining)
	}

	// 不同 key 相互独立。
	if allowed, _, _ := store.Take("k2", 1, time.Minute, now); !allowed {
		t.Fatalf("独立 key 首次应放行")
	}

	// 清理过期计数：把 now 推进到两窗口之后，过期行应被删除。
	if err := purgeExpiredRateLimits(a.DB, now.Add(10*time.Minute)); err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	var cnt int64
	a.DB.Model(&models.RateLimitCounter{}).Count(&cnt)
	if cnt != 0 {
		t.Fatalf("过期计数应被清理，实际剩 %d 行", cnt)
	}
}
