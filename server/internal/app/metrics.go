package app

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/cluster"
	"knowforge/server/internal/jobqueue"
	"knowforge/server/internal/metrics"
	"knowforge/server/internal/models"
)

// —— 运行指标：GET /metrics 以 Prometheus 文本格式输出，默认关闭；开启后需在请求头携带访问令牌（Authorization: Bearer <令牌>）。
// 请求量与耗时、事件流连接、模型调用为本实例的计数（多实例时逐个实例抓取）；任务积压、在线实例与数据总量来自数据库，
// 各实例相同。令牌只保存哈希，生成时显示一次。——

const cfgMetricsSettings = "metrics_settings"

var (
	httpRequests = metrics.Default.NewCounterVec("knowforge_http_requests_total",
		"HTTP requests handled by this instance.", "method", "route", "status")
	httpDuration = metrics.Default.NewHistogramVec("knowforge_http_request_duration_seconds",
		"HTTP request latency (event streams excluded).", []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}, "method", "route")
	httpInFlight = metrics.Default.NewGaugeVec("knowforge_http_requests_in_flight", "HTTP requests currently being served.")
	aiCalls      = metrics.Default.NewCounterVec("knowforge_ai_calls_total",
		"Model calls made by this instance.", "feature", "kind", "model", "status")
	aiTokens = metrics.Default.NewCounterVec("knowforge_ai_tokens_total",
		"Model tokens used by this instance.", "kind", "direction")
	aiCost = metrics.Default.NewCounterVec("knowforge_ai_cost_total",
		"Estimated model cost on this instance, by currency.", "currency")
	aiDuration = metrics.Default.NewHistogramVec("knowforge_ai_call_duration_seconds",
		"Model call latency.", []float64{0.5, 1, 2, 5, 10, 20, 30, 60, 120, 300}, "kind")

	metricsApp     atomic.Pointer[App]
	processStarted = time.Now()
)

func init() {
	metrics.Default.Collect(collectRuntimeMetrics)
	metrics.Default.Collect(func(w *metrics.Writer) {
		if a := metricsApp.Load(); a != nil {
			a.collectDBMetrics(w)
		}
	})
}

// metricsMiddleware 记录请求数、耗时与正在处理的请求数。路由取注册时的路径模板（如 /api/v1/books/:id），跨域预检记为 preflight，
// 未匹配的 API 请求记为 unmatched，页面请求记为 web，避免标签数量无限增长。
func metricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		httpInFlight.Add(1)
		c.Next()
		httpInFlight.Add(-1)
		route := c.FullPath()
		if route == "" {
			switch {
			case c.Request.Method == http.MethodOptions:
				route = "preflight" // 跨域预检由 CORS 中间件直接应答
			case strings.HasPrefix(c.Request.URL.Path, "/api/"):
				route = "unmatched"
			default:
				route = "web"
			}
		}
		httpRequests.Inc(c.Request.Method, route, strconv.Itoa(c.Writer.Status()))
		if !strings.HasPrefix(c.Writer.Header().Get("Content-Type"), "text/event-stream") {
			httpDuration.Observe(time.Since(start).Seconds(), c.Request.Method, route)
		}
	}
}

// observeAICall 记录一次模型调用（由用量记录统一调用，所有模型调用都会经过）。
func observeAICall(row *models.AIUsageLog, elapsed time.Duration) {
	aiCalls.Inc(row.Feature, row.Kind, row.Model, row.Status)
	aiDuration.Observe(elapsed.Seconds(), row.Kind)
	if row.Status == "ok" {
		aiTokens.Add(float64(row.InputTokens), row.Kind, "input")
		aiTokens.Add(float64(row.OutputTokens), row.Kind, "output")
		if row.CostMicros > 0 && row.Currency != "" {
			aiCost.Add(float64(row.CostMicros)/1e6, row.Currency)
		}
	}
}

func collectRuntimeMetrics(w *metrics.Writer) {
	w.Gauge("knowforge_build_info", "Build information.", 1, "version", Version, "go_version", runtime.Version())
	w.Gauge("knowforge_start_time_seconds", "Process start time (unix seconds).", float64(processStarted.Unix()))
	w.Gauge("go_goroutines", "Number of goroutines.", float64(runtime.NumGoroutine()))
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	w.Gauge("go_memstats_heap_alloc_bytes", "Heap bytes allocated and in use.", float64(ms.HeapAlloc))
	w.Gauge("go_memstats_sys_bytes", "Bytes obtained from the system.", float64(ms.Sys))
	w.Header("go_gc_cycles_total", "Completed GC cycles.", "counter")
	w.Sample("go_gc_cycles_total", float64(ms.NumGC))
}

// 数据总量变化慢、统计代价较高（大表 COUNT），缓存一分钟（按应用实例区分）。
var totalsCache struct {
	sync.Mutex
	owner                   *App
	at                      time.Time
	users, books, documents int64
}

func (a *App) collectDBMetrics(w *metrics.Writer) {
	installed := 0.0
	if a.Config.Installed && a.DB != nil {
		installed = 1
	}
	w.Gauge("knowforge_installed", "Whether the site has been installed.", installed)
	if installed == 0 {
		return
	}
	if sqlDB, err := a.DB.DB(); err == nil {
		st := sqlDB.Stats()
		w.Header("knowforge_db_connections", "Database connections by state.", "gauge")
		w.Sample("knowforge_db_connections", float64(st.InUse), "state", "in_use")
		w.Sample("knowforge_db_connections", float64(st.Idle), "state", "idle")
		w.Header("knowforge_db_wait_total", "Times a query waited for a free connection.", "counter")
		w.Sample("knowforge_db_wait_total", float64(st.WaitCount))
		w.Header("knowforge_db_wait_seconds_total", "Total time spent waiting for a free connection.", "counter")
		w.Sample("knowforge_db_wait_seconds_total", st.WaitDuration.Seconds())
	}

	// 任务队列：按类型与状态的数量（不含已成功的），以及最早一个可执行但仍在等待的任务已等待的秒数
	var rows []struct {
		Type, Status string
		N            int64
	}
	a.DB.Model(&models.BackgroundJob{}).Select("type, status, COUNT(*) AS n").
		Where("status IN ?", []string{jobqueue.StatusPending, jobqueue.StatusRetrying, jobqueue.StatusRunning, jobqueue.StatusFailed}).
		Group("type, status").Scan(&rows)
	w.Header("knowforge_jobs", "Background jobs by type and status (succeeded jobs excluded).", "gauge")
	for _, r := range rows {
		w.Sample("knowforge_jobs", float64(r.N), "type", r.Type, "status", r.Status)
	}
	var oldest struct{ T *time.Time }
	now := time.Now()
	a.DB.Model(&models.BackgroundJob{}).Select("MIN(available_at) AS t").
		Where("status IN ? AND available_at <= ?", []string{jobqueue.StatusPending, jobqueue.StatusRetrying}, now).Scan(&oldest)
	wait := 0.0
	if oldest.T != nil && !oldest.T.IsZero() {
		wait = now.Sub(*oldest.T).Seconds()
	}
	w.Gauge("knowforge_jobs_oldest_waiting_seconds", "How long the oldest runnable job has been waiting.", wait)

	instances := len(cluster.Instances())
	if instances == 0 {
		instances = 1 // 未登记多实例时只有本实例
	}
	w.Gauge("knowforge_cluster_instances", "Instances currently online.", float64(instances))

	totalsCache.Lock()
	if totalsCache.owner != a || time.Since(totalsCache.at) > time.Minute {
		a.DB.Model(&models.User{}).Count(&totalsCache.users)
		a.DB.Model(&models.Book{}).Count(&totalsCache.books)
		a.DB.Model(&models.Document{}).Count(&totalsCache.documents)
		totalsCache.owner, totalsCache.at = a, time.Now()
	}
	users, books, documents := totalsCache.users, totalsCache.books, totalsCache.documents
	totalsCache.Unlock()
	w.Gauge("knowforge_users", "Registered users.", float64(users))
	w.Gauge("knowforge_books", "Books.", float64(books))
	w.Gauge("knowforge_documents", "Chapters.", float64(documents))
}

// —— 设置与访问令牌 ——

type metricsSettings struct {
	Enabled   bool   `json:"enabled"`
	TokenHash string `json:"token_hash"`
	TokenHint string `json:"token_hint"` // 令牌末 4 位，用于辨认
}

func (a *App) metricsSettings() metricsSettings {
	var s metricsSettings
	if raw := a.getSetting(cfgMetricsSettings); raw != "" {
		_ = json.Unmarshal([]byte(raw), &s)
	}
	return s
}

func (a *App) saveMetricsSettings(s metricsSettings) error {
	raw, _ := json.Marshal(s)
	return a.setSetting(cfgMetricsSettings, string(raw), "运行指标：开关与访问令牌（哈希）")
}

func hashMetricsToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newMetricsToken() string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	return "kfm_" + hex.EncodeToString(buf)
}

// ServeMetrics GET /metrics 运行指标（未开启时 404；令牌错误时 401）。
func (a *App) ServeMetrics(c *gin.Context) {
	if !a.Config.Installed || a.DB == nil {
		c.String(http.StatusNotFound, "404 page not found")
		return
	}
	s := a.metricsSettings()
	if !s.Enabled || s.TokenHash == "" {
		c.String(http.StatusNotFound, "404 page not found")
		return
	}
	token, found := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
	if !found || subtle.ConstantTimeCompare([]byte(hashMetricsToken(strings.TrimSpace(token))), []byte(s.TokenHash)) != 1 {
		c.Header("WWW-Authenticate", `Bearer realm="knowforge-metrics"`)
		c.String(http.StatusUnauthorized, "unauthorized")
		return
	}
	a.writeMetrics(c)
}

func (a *App) writeMetrics(c *gin.Context) {
	var buf bytes.Buffer
	if err := metrics.Default.Write(&buf); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/plain; version=0.0.4; charset=utf-8", buf.Bytes())
}

func metricsSettingsView(s metricsSettings) gin.H {
	return gin.H{"enabled": s.Enabled, "token_set": s.TokenHash != "", "token_hint": s.TokenHint, "path": "/metrics"}
}

// AdminGetMetricsSettings GET /metrics/settings 运行指标开关与令牌状态（不返回令牌）。
func (a *App) AdminGetMetricsSettings(c *gin.Context) {
	ok(c, metricsSettingsView(a.metricsSettings()))
}

// AdminSaveMetricsSettings PUT /metrics/settings {enabled}；首次开启时生成令牌，响应中的 token 只出现这一次。
func (a *App) AdminSaveMetricsSettings(c *gin.Context) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if c.ShouldBindJSON(&req) != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	s := a.metricsSettings()
	s.Enabled = req.Enabled
	token := ""
	if s.Enabled && s.TokenHash == "" {
		token = newMetricsToken()
		s.TokenHash, s.TokenHint = hashMetricsToken(token), token[len(token)-4:]
	}
	if err := a.saveMetricsSettings(s); err != nil {
		fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	a.recordAudit(c, "metrics.settings", "config", "metrics", "运行指标", map[string]any{"enabled": s.Enabled})
	view := metricsSettingsView(s)
	if token != "" {
		view["token"] = token
	}
	ok(c, view)
}

// AdminRegenerateMetricsToken POST /metrics/token 重新生成令牌（旧令牌立即失效），响应中的 token 只出现这一次。
func (a *App) AdminRegenerateMetricsToken(c *gin.Context) {
	s := a.metricsSettings()
	token := newMetricsToken()
	s.TokenHash, s.TokenHint = hashMetricsToken(token), token[len(token)-4:]
	if err := a.saveMetricsSettings(s); err != nil {
		fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	a.recordAudit(c, "metrics.token", "config", "metrics", "运行指标令牌", nil)
	view := metricsSettingsView(s)
	view["token"] = token
	ok(c, view)
}

// AdminPreviewMetrics GET /metrics/preview 管理员预览本实例当前的指标输出（无需令牌）。
func (a *App) AdminPreviewMetrics(c *gin.Context) {
	a.writeMetrics(c)
}
