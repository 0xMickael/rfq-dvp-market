package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPercentilesUseNearestRankAndKeepInput(t *testing.T) {
	samples := []float64{50, 10, 40, 20, 30}
	got := percentiles(samples)
	if got.Count != 5 || got.P50 != 30 || got.P95 != 50 || got.P99 != 50 || got.Max != 50 {
		t.Fatal(got)
	}
	if samples[0] != 50 {
		t.Fatal("samples modified")
	}
	if percentiles(nil).Count != 0 {
		t.Fatal("empty distribution")
	}
}
func TestTechnicalFailuresAreNeverBusinessRejections(t *testing.T) {
	for _, code := range []string{"ENGINE_UNAVAILABLE", "DATABASE_UNAVAILABLE", "RECOVERY_REQUIRED", "INVALID_SIGNATURE"} {
		if businessError(&apiFailure{Status: 503, Code: code}) {
			t.Fatal(code)
		}
	}
	if !businessError(&apiFailure{Status: 409, Code: "INSUFFICIENT_INVENTORY"}) {
		t.Fatal("inventory rejection")
	}
	if businessError(context.DeadlineExceeded) {
		t.Fatal("timeout classified as business rejection")
	}
}
func TestRequestCancellationReachesServer(t *testing.T) {
	cancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done(); close(cancelled) }))
	defer server.Close()
	c := &client{http: server.Client(), options: options{API: server.URL}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.call(ctx, "/api/rfqs", nil, new(any)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%v", err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("request not cancelled")
	}
}
