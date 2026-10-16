package server

import (
	"fmt"
	"io"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics is a small Prometheus text exporter, so the runtime has no metrics dependency.
type Metrics struct {
	mu        sync.Mutex
	requests  map[string]*atomic.Int64 // gateway
	errors    map[string]*atomic.Int64 // gateway
	latency   map[string]*histogram    // gateway
	started   atomic.Int64
	ended     atomic.Int64
	actions   map[string]*atomic.Int64 // result
	actionLat histogram
}

var buckets = []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

type histogram struct {
	mu     sync.Mutex
	counts [13]int64 // one per bucket + overflow
	sum    float64
	n      int64
}

func (h *histogram) observe(d time.Duration) {
	s := d.Seconds()
	h.mu.Lock()
	i := sort.SearchFloat64s(buckets, s)
	h.counts[i]++
	h.sum += s
	h.n++
	h.mu.Unlock()
}

func NewMetrics() *Metrics {
	return &Metrics{
		requests: map[string]*atomic.Int64{},
		errors:   map[string]*atomic.Int64{},
		latency:  map[string]*histogram{},
		actions:  map[string]*atomic.Int64{},
	}
}

func (m *Metrics) counter(set map[string]*atomic.Int64, k string) *atomic.Int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := set[k]
	if !ok {
		c = &atomic.Int64{}
		set[k] = c
	}
	return c
}

func (m *Metrics) Request(gw string, d time.Duration, isNew, ended, failed bool) {
	m.counter(m.requests, gw).Add(1)
	if failed {
		m.counter(m.errors, gw).Add(1)
	}
	if isNew {
		m.started.Add(1)
	}
	if ended {
		m.ended.Add(1)
	}
	m.mu.Lock()
	h, ok := m.latency[gw]
	if !ok {
		h = &histogram{}
		m.latency[gw] = h
	}
	m.mu.Unlock()
	h.observe(d)
}

func (m *Metrics) Action(d time.Duration, err error) {
	result := "ok"
	if err != nil {
		result = "error"
	}
	m.counter(m.actions, result).Add(1)
	m.actionLat.observe(d)
}

func (m *Metrics) Write(w io.Writer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fmt.Fprintln(w, "# HELP ussd_requests_total Gateway requests handled.\n# TYPE ussd_requests_total counter")
	for _, k := range keys(m.requests) {
		fmt.Fprintf(w, "ussd_requests_total{gateway=%q} %d\n", k, m.requests[k].Load())
	}
	fmt.Fprintln(w, "# HELP ussd_request_errors_total Requests that failed (bad request or storage error).\n# TYPE ussd_request_errors_total counter")
	for _, k := range keys(m.errors) {
		fmt.Fprintf(w, "ussd_request_errors_total{gateway=%q} %d\n", k, m.errors[k].Load())
	}
	fmt.Fprintf(w, "# TYPE ussd_sessions_started_total counter\nussd_sessions_started_total %d\n", m.started.Load())
	fmt.Fprintf(w, "# TYPE ussd_sessions_ended_total counter\nussd_sessions_ended_total %d\n", m.ended.Load())
	fmt.Fprintln(w, "# HELP ussd_request_duration_seconds Time to answer a gateway request, including any action call.\n# TYPE ussd_request_duration_seconds histogram")
	lat := make([]string, 0, len(m.latency))
	for k := range m.latency {
		lat = append(lat, k)
	}
	sort.Strings(lat)
	for _, k := range lat {
		writeHist(w, "ussd_request_duration_seconds", fmt.Sprintf("gateway=%q", k), m.latency[k])
	}
	fmt.Fprintln(w, "# TYPE ussd_actions_total counter")
	for _, k := range keys(m.actions) {
		fmt.Fprintf(w, "ussd_actions_total{result=%q} %d\n", k, m.actions[k].Load())
	}
	fmt.Fprintln(w, "# TYPE ussd_action_duration_seconds histogram")
	writeHist(w, "ussd_action_duration_seconds", "", &m.actionLat)
}

func writeHist(w io.Writer, name, labels string, h *histogram) {
	h.mu.Lock()
	defer h.mu.Unlock()
	sep := ""
	if labels != "" {
		sep = ","
	}
	var cum int64
	for i, b := range buckets {
		cum += h.counts[i]
		fmt.Fprintf(w, "%s_bucket{%s%sle=\"%g\"} %d\n", name, labels, sep, b, cum)
	}
	fmt.Fprintf(w, "%s_bucket{%s%sle=\"+Inf\"} %d\n", name, labels, sep, h.n)
	if labels != "" {
		labels = "{" + labels + "}"
	}
	fmt.Fprintf(w, "%s_sum%s %g\n%s_count%s %d\n", name, labels, h.sum, name, labels, h.n)
}

func keys(m map[string]*atomic.Int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
