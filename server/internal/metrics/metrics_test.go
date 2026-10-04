package metrics

import (
	"bytes"
	"strings"
	"testing"
)

// 文本格式：HELP / TYPE、标签转义、样本按标签排序、直方图累计桶与 _sum / _count、采集函数。
func TestRegistryWrite(t *testing.T) {
	r := &Registry{}
	c := r.NewCounterVec("app_requests_total", "Requests.\nTotal", "method", "path")
	c.Inc("GET", "/b")
	c.Add(2, "GET", "/a")
	c.Add(-1, "GET", "/a") // 计数器不减
	c.Inc("POST", `/x"y\z`)
	g := r.NewGaugeVec("app_inflight", "In flight.")
	g.Add(3)
	g.Add(-1)
	h := r.NewHistogramVec("app_duration_seconds", "Duration.", []float64{1, 0.1}, "route")
	h.Observe(0.05, "r")
	h.Observe(0.5, "r")
	h.Observe(3, "r")
	r.Collect(func(w *Writer) { w.Gauge("app_users", "Users.", 7) })

	var buf bytes.Buffer
	if err := r.Write(&buf); err != nil {
		t.Fatal(err)
	}
	want := `# HELP app_requests_total Requests.\nTotal
# TYPE app_requests_total counter
app_requests_total{method="GET",path="/a"} 2
app_requests_total{method="GET",path="/b"} 1
app_requests_total{method="POST",path="/x\"y\\z"} 1
# HELP app_inflight In flight.
# TYPE app_inflight gauge
app_inflight 2
# HELP app_duration_seconds Duration.
# TYPE app_duration_seconds histogram
app_duration_seconds_bucket{route="r",le="0.1"} 1
app_duration_seconds_bucket{route="r",le="1"} 2
app_duration_seconds_bucket{route="r",le="+Inf"} 3
app_duration_seconds_sum{route="r"} 3.55
app_duration_seconds_count{route="r"} 3
# HELP app_users Users.
# TYPE app_users gauge
app_users 7
`
	if got := buf.String(); got != want {
		t.Fatalf("输出不符:\n%s\n--- 期望 ---\n%s", got, want)
	}
	if !strings.HasSuffix(want, "\n") {
		t.Fatal("输出应以换行结尾")
	}
}
