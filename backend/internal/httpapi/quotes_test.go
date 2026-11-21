package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"rfq-dvp-market.local/backend/internal/engine"
	"strings"
	"testing"
	"time"
)

func quoteRequest(body string) *http.Request {
	r := httptest.NewRequest("POST", "/api/quotes", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestQuotesPreserveAmountsAndRefusals(t *testing.T) {
	const input = `{"side":"SELL","unit":"sEUR","amount":"9007199254.740993"}`
	for _, status := range []int{200, 400, 422} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if r.Method != "POST" || r.URL.Path != "/quotes" || string(body) != input {
					t.Errorf("altered request: %s", body)
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"fixture","rejections":[]}`))
			}))
			defer upstream.Close()
			response := httptest.NewRecorder()
			Handler(engine.NewClient(upstream.URL)).ServeHTTP(response, quoteRequest(input))
			if response.Code != status || response.Body.String() != `{"message":"fixture","rejections":[]}` {
				t.Fatalf("%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestInvalidRequestsNeverCallEngine(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected call") }))
	defer upstream.Close()
	for _, body := range []string{`{}`, `null`, `{"side":"BUY","unit":"tBILL","amount":100}`, `{"side":"OTHER","unit":"tBILL","amount":"1"}`, `{"side":"BUY","unit":"tBILL","amount":"1","extra":true}`, `{"side":"BUY","unit":"tBILL","amount":"1"}{}`, strings.Repeat("x", 5000)} {
		response := httptest.NewRecorder()
		Handler(engine.NewClient(upstream.URL)).ServeHTTP(response, quoteRequest(body))
		if response.Code != 400 {
			t.Fatalf("status %d for %s", response.Code, body)
		}
	}
}

func TestEngineFailures(t *testing.T) {
	for _, mode := range []string{"timeout", "invalid-json", "status", "oversized", "closed"} {
		t.Run(mode, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "timeout":
					select {
					case <-r.Context().Done():
					case <-time.After(200 * time.Millisecond):
					}
				case "invalid-json":
					_, _ = w.Write([]byte("broken"))
				case "status":
					w.WriteHeader(500)
					_, _ = w.Write([]byte(`{}`))
				case "oversized":
					_, _ = w.Write([]byte(`"` + strings.Repeat("x", 65536) + `"`))
				}
			}))
			defer upstream.Close()
			if mode == "closed" {
				upstream.Close()
			}
			client := engine.NewClient(upstream.URL)
			client.HTTP.Timeout = 50 * time.Millisecond
			response := httptest.NewRecorder()
			Handler(client).ServeHTTP(response, quoteRequest(`{"side":"BUY","unit":"tBILL","amount":"1"}`))
			if response.Code != 503 || !strings.Contains(response.Body.String(), "ENGINE_UNAVAILABLE") {
				t.Fatalf("%d %s", response.Code, response.Body.String())
			}
		})
	}
}
