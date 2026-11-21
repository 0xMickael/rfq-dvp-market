package main

import (
	"errors"
	"math"
	"sort"
	"sync"
	"time"
)

type distribution struct {
	Count int     `json:"count"`
	P50   float64 `json:"p50_ms"`
	P95   float64 `json:"p95_ms"`
	P99   float64 `json:"p99_ms"`
	Max   float64 `json:"max_ms"`
}

func percentiles(samples []float64) distribution {
	if len(samples) == 0 {
		return distribution{}
	}
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	percentile := func(q float64) float64 { return sorted[int(math.Ceil(q*float64(len(sorted))))-1] }
	return distribution{len(sorted), percentile(.5), percentile(.95), percentile(.99), sorted[len(sorted)-1]}
}

type stageReport struct {
	Attempted      int            `json:"attempted"`
	Successful     int            `json:"successful"`
	Rejected       int            `json:"business_rejected"`
	Errors         int            `json:"technical_errors"`
	Codes          map[string]int `json:"codes"`
	Latency        distribution   `json:"latency_all_responses"`
	SuccessLatency distribution   `json:"latency_success"`
}
type stageSamples struct {
	report       stageReport
	all, success []float64
}
type invariantReport struct {
	Passed      bool     `json:"passed"`
	CheckedRFQs int      `json:"checked_rfqs"`
	Trades      int      `json:"trades"`
	Settled     int      `json:"settled"`
	Pending     int      `json:"pending"`
	Failed      int      `json:"failed"`
	Errors      []string `json:"errors"`
}
type report struct {
	Options           options                `json:"options"`
	Started           time.Time              `json:"started_utc"`
	AdmissionSeconds  float64                `json:"admission_seconds"`
	CompletionSeconds float64                `json:"completion_seconds"`
	WarmupRFQs        int                    `json:"warmup_rfqs"`
	Attempted         int                    `json:"operations_attempted"`
	Completed         int                    `json:"operations_completed"`
	Success           int                    `json:"successful_operations"`
	Rejected          int                    `json:"business_rejected_operations"`
	Errors            int                    `json:"technical_error_operations"`
	Ignored           int                    `json:"quotes_ignored"`
	Quotes            int                    `json:"quotes_received"`
	DistinctTraders   int                    `json:"distinct_traders_with_rfq"`
	BuyRFQs           int                    `json:"buy_rfqs_created"`
	SellRFQs          int                    `json:"sell_rfqs_created"`
	Accepted          int                    `json:"acceptances_acknowledged"`
	Cancelled         bool                   `json:"cancelled"`
	Throughput        float64                `json:"completed_operations_per_second_including_drain"`
	Stages            map[string]stageReport `json:"stages"`
	Invariants        invariantReport        `json:"invariants"`
}
type measurements struct {
	mu     sync.Mutex
	stages map[string]*stageSamples
}

func businessError(err error) bool {
	var failure *apiFailure
	if !errors.As(err, &failure) {
		return false
	}
	switch failure.Code {
	case "NO_EXECUTABLE_QUOTE", "INSUFFICIENT_INVENTORY", "QUOTE_EXPIRED", "QUOTE_UNAVAILABLE", "SETTLEMENT_DEADLINE", "ALREADY_ACCEPTED":
		return true
	}
	return false
}
func (m *measurements) observe(stage string, started time.Time, err error) {
	elapsed := float64(time.Since(started)) / float64(time.Millisecond)
	m.mu.Lock()
	defer m.mu.Unlock()
	sample := m.stages[stage]
	if sample == nil {
		sample = &stageSamples{report: stageReport{Codes: map[string]int{}}}
		m.stages[stage] = sample
	}
	sample.report.Attempted++
	sample.all = append(sample.all, elapsed)
	code := "success"
	if err == nil {
		sample.report.Successful++
		sample.success = append(sample.success, elapsed)
	} else {
		if businessError(err) {
			sample.report.Rejected++
		} else {
			sample.report.Errors++
		}
		var failure *apiFailure
		if errors.As(err, &failure) {
			code = failure.Code
		} else {
			code = "transport_or_validation_error"
		}
	}
	sample.report.Codes[code]++
}
func (m *measurements) finish() map[string]stageReport {
	result := map[string]stageReport{}
	for name, sample := range m.stages {
		value := sample.report
		value.Latency = percentiles(sample.all)
		value.SuccessLatency = percentiles(sample.success)
		result[name] = value
	}
	return result
}
