package httpapi

import (
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"rfq-dvp-market.local/backend/internal/settlement"
)

var latencyBounds = []float64{0.005, 0.025, 0.1, 0.5, 2, 5}

type httpSample struct {
	count   uint64
	seconds float64
	buckets [6]uint64
}
type httpMetrics struct {
	mu      sync.Mutex
	samples map[string]*httpSample
}
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func instrument(mux *http.ServeMux, manager *settlement.Manager) http.Handler {
	metrics := &httpMetrics{samples: map[string]*httpSample{}}
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) { metrics.expose(w, manager) })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			mux.ServeHTTP(w, r)
			return
		}
		started := time.Now()
		writer := &statusWriter{ResponseWriter: w, status: 200}
		mux.ServeHTTP(writer, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		key := fmt.Sprintf("route=%q,status=\"%dxx\"", route, writer.status/100)
		seconds := time.Since(started).Seconds()
		metrics.mu.Lock()
		defer metrics.mu.Unlock()
		sample := metrics.samples[key]
		if sample == nil {
			sample = &httpSample{}
			metrics.samples[key] = sample
		}
		sample.count++
		sample.seconds += seconds
		for i, bound := range latencyBounds {
			if seconds <= bound {
				sample.buckets[i]++
			}
		}
	})
}
func (m *httpMetrics) expose(w http.ResponseWriter, manager *settlement.Manager) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	m.mu.Lock()
	fmt.Fprintln(w, "# TYPE rfq_http_requests_total counter\n# TYPE rfq_http_request_duration_seconds histogram")
	keys := make([]string, 0, len(m.samples))
	for key := range m.samples {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		s := m.samples[key]
		fmt.Fprintf(w, "rfq_http_requests_total{%s} %d\n", key, s.count)
		for i, bound := range latencyBounds {
			fmt.Fprintf(w, "rfq_http_request_duration_seconds_bucket{%s,le=\"%g\"} %d\n", key, bound, s.buckets[i])
		}
		fmt.Fprintf(w, "rfq_http_request_duration_seconds_bucket{%s,le=\"+Inf\"} %d\nrfq_http_request_duration_seconds_sum{%s} %g\nrfq_http_request_duration_seconds_count{%s} %d\n", key, s.count, key, s.seconds, key, s.count)
	}
	m.mu.Unlock()
	if manager != nil {
		pending, settled, failed, attempts, retries := manager.Metrics()
		fmt.Fprintf(w, "# TYPE rfq_settlement_jobs gauge\nrfq_settlement_jobs{status=\"pending\"} %d\nrfq_settlement_jobs{status=\"settled\"} %d\nrfq_settlement_jobs{status=\"failed\"} %d\n# TYPE rfq_settlement_attempts_total counter\nrfq_settlement_attempts_total %d\n# TYPE rfq_settlement_retries_total counter\nrfq_settlement_retries_total %d\n", pending, settled, failed, attempts, retries)
	}
}
