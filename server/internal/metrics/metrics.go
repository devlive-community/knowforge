// Package metrics 运行指标：不依赖第三方库的计数器、仪表与直方图，按 Prometheus 文本格式（0.0.4）输出。
// 进程内指标（请求、事件流连接、模型调用）由各处直接记录；数据库中的统计（任务积压、在线实例等）由采集函数在抓取时计算。
package metrics

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const sep = "\xff"

type kind string

const (
	kindCounter   kind = "counter"
	kindGauge     kind = "gauge"
	kindHistogram kind = "histogram"
)

// Registry 指标集合。
type Registry struct {
	mu         sync.Mutex
	families   []*family
	collectors []func(w *Writer)
}

type family struct {
	name, help string
	kind       kind
	labels     []string
	buckets    []float64
	mu         sync.Mutex
	series     map[string]*series
}

type series struct {
	values []string
	value  float64  // 计数器 / 仪表
	counts []uint64 // 直方图各桶（非累计）
	sum    float64  // 直方图总和
	count  uint64   // 直方图样本数
}

// Default 全局指标集合。
var Default = &Registry{}

func (r *Registry) register(f *family) *family {
	f.series = map[string]*series{}
	r.mu.Lock()
	r.families = append(r.families, f)
	r.mu.Unlock()
	return f
}

// Collect 登记一个抓取时调用的采集函数（用于需要现查的指标）。
func (r *Registry) Collect(fn func(w *Writer)) {
	r.mu.Lock()
	r.collectors = append(r.collectors, fn)
	r.mu.Unlock()
}

func (f *family) get(values []string) *series {
	if len(values) != len(f.labels) {
		panic(fmt.Sprintf("metrics: %s 需要 %d 个标签值，实际 %d 个", f.name, len(f.labels), len(values)))
	}
	key := strings.Join(values, sep)
	s := f.series[key]
	if s == nil {
		s = &series{values: append([]string(nil), values...)}
		if f.kind == kindHistogram {
			s.counts = make([]uint64, len(f.buckets))
		}
		f.series[key] = s
	}
	return s
}

// CounterVec 带标签的计数器（只增）。
type CounterVec struct{ f *family }

func (r *Registry) NewCounterVec(name, help string, labels ...string) *CounterVec {
	return &CounterVec{r.register(&family{name: name, help: help, kind: kindCounter, labels: labels})}
}

func (c *CounterVec) Add(v float64, values ...string) {
	if v < 0 {
		return
	}
	c.f.mu.Lock()
	c.f.get(values).value += v
	c.f.mu.Unlock()
}

func (c *CounterVec) Inc(values ...string) { c.Add(1, values...) }

// GaugeVec 带标签的仪表（可增可减）。
type GaugeVec struct{ f *family }

func (r *Registry) NewGaugeVec(name, help string, labels ...string) *GaugeVec {
	return &GaugeVec{r.register(&family{name: name, help: help, kind: kindGauge, labels: labels})}
}

func (g *GaugeVec) Add(v float64, values ...string) {
	g.f.mu.Lock()
	g.f.get(values).value += v
	g.f.mu.Unlock()
}

func (g *GaugeVec) Set(v float64, values ...string) {
	g.f.mu.Lock()
	g.f.get(values).value = v
	g.f.mu.Unlock()
}

// HistogramVec 带标签的直方图。
type HistogramVec struct{ f *family }

func (r *Registry) NewHistogramVec(name, help string, buckets []float64, labels ...string) *HistogramVec {
	b := append([]float64(nil), buckets...)
	sort.Float64s(b)
	return &HistogramVec{r.register(&family{name: name, help: help, kind: kindHistogram, labels: labels, buckets: b})}
}

func (h *HistogramVec) Observe(v float64, values ...string) {
	h.f.mu.Lock()
	s := h.f.get(values)
	for i, upper := range h.f.buckets {
		if v <= upper {
			s.counts[i]++
			break
		}
	}
	s.sum += v
	s.count++
	h.f.mu.Unlock()
}

// —— 输出 ——

// Writer 按 Prometheus 文本格式写出指标；采集函数用它写出现查的指标。
type Writer struct {
	w   io.Writer
	err error
}

func (w *Writer) printf(format string, args ...any) {
	if w.err == nil {
		_, w.err = fmt.Fprintf(w.w, format, args...)
	}
}

// Header 写出一个指标的 HELP 与 TYPE 行。
func (w *Writer) Header(name, help, typ string) {
	w.printf("# HELP %s %s\n# TYPE %s %s\n", name, escapeHelp(help), name, typ)
}

// Sample 写出一个样本；labels 为「名, 值, 名, 值…」。
func (w *Writer) Sample(name string, value float64, labels ...string) {
	w.printf("%s%s %s\n", name, formatLabels(labels), formatValue(value))
}

// Gauge 写出只有一个样本的仪表（含 HELP / TYPE）。
func (w *Writer) Gauge(name, help string, value float64, labels ...string) {
	w.Header(name, help, "gauge")
	w.Sample(name, value, labels...)
}

// Write 写出全部指标：先运行采集函数，再写出各指标（同一指标的样本按标签值排序，输出稳定）。
func (r *Registry) Write(out io.Writer) error {
	w := &Writer{w: out}
	r.mu.Lock()
	families := append([]*family(nil), r.families...)
	collectors := append([]func(*Writer){}, r.collectors...)
	r.mu.Unlock()
	for _, f := range families {
		f.write(w)
	}
	for _, fn := range collectors {
		fn(w)
	}
	return w.err
}

func (f *family) write(w *Writer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header(f.name, f.help, string(f.kind))
	keys := make([]string, 0, len(f.series))
	for k := range f.series {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s := f.series[k]
		labels := make([]string, 0, len(f.labels)*2+2)
		for i, name := range f.labels {
			labels = append(labels, name, s.values[i])
		}
		if f.kind != kindHistogram {
			w.Sample(f.name, s.value, labels...)
			continue
		}
		var cum uint64
		for i, upper := range f.buckets {
			cum += s.counts[i]
			w.Sample(f.name+"_bucket", float64(cum), append(labels, "le", formatValue(upper))...)
		}
		w.Sample(f.name+"_bucket", float64(s.count), append(labels, "le", "+Inf")...)
		w.Sample(f.name+"_sum", s.sum, labels...)
		w.Sample(f.name+"_count", float64(s.count), labels...)
	}
}

func formatLabels(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i+1 < len(labels); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(labels[i])
		b.WriteString(`="`)
		b.WriteString(escapeLabel(labels[i+1]))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
var helpEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`)

func escapeLabel(s string) string { return labelEscaper.Replace(s) }
func escapeHelp(s string) string  { return helpEscaper.Replace(s) }

func formatValue(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}
