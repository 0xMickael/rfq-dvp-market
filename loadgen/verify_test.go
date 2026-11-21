package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rfq-dvp-market.local/backend/internal/engine"
)

func TestAuditRefusesLostAcknowledgedTrade(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"rfq_id":"rfq-1","trader_id":"alice","trade":null}`))
	}))
	defer server.Close()
	run := &campaign{
		client:  &client{http: server.Client(), options: options{API: server.URL, Mode: "settlement"}},
		records: []recordedRFQ{{rfq: engine.Rfq{RfqID: "rfq-1", TraderID: "alice"}, acknowledged: true}},
		report:  report{Accepted: 1},
	}
	result := run.verify(context.Background(), baseline{})
	if result.Passed || !strings.Contains(strings.Join(result.Errors, " "), "acknowledged acceptance without a durable trade") {
		t.Fatalf("%+v", result)
	}
}
func TestAuditPollingDoesNotChangeMeasuredSamples(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"trade_id":"trade-1","status":"SETTLED"}`))
	}))
	defer server.Close()
	run := &campaign{client: &client{http: server.Client(), options: options{API: server.URL}}, measurements: measurements{stages: map[string]*stageSamples{}}}
	if _, err := run.waitSettlement(context.Background(), "trade-1", false); err != nil {
		t.Fatal(err)
	}
	if len(run.measurements.stages) != 0 {
		t.Fatal("audit included in measurements")
	}
}
