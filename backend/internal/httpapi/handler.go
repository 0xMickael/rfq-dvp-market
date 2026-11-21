package httpapi

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"rfq-dvp-market.local/backend/internal/engine"
	"rfq-dvp-market.local/backend/internal/settlement"
)

func NewHandler() http.Handler { return Handler(engine.NewClient("http://127.0.0.1:8081")) }

func Handler(client *engine.Client, signing ...*SigningService) http.Handler {
	mux := http.NewServeMux()
	var service *SigningService
	if len(signing) != 0 {
		service = signing[0]
	}
	registerRFQs(mux, service)
	mux.HandleFunc("GET /api/market", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		status, body, err := client.Call(r.Context(), "GET", "/market", nil)
		if err != nil {
			w.WriteHeader(503)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "Market unavailable"})
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("POST /api/quotes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		fail := func(status int, code, message string) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "message": message, "rejections": []any{}})
		}
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" {
			fail(415, "INVALID_REQUEST", "Content-Type application/json is required.")
			return
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		var input engine.Request
		if err := decoder.Decode(&input); err != nil {
			fail(400, "INVALID_REQUEST", "Invalid JSON body.")
			return
		}
		if decoder.Decode(new(any)) != io.EOF || (input.Side != "BUY" && input.Side != "SELL") || (input.Unit != "tBILL" && input.Unit != "sEUR") || input.Amount == "" {
			fail(400, "INVALID_REQUEST", "side BUY/SELL, unit tBILL/sEUR and a string amount are required.")
			return
		}
		status, body, err := client.Quote(r.Context(), input)
		if err != nil {
			fail(503, "ENGINE_UNAVAILABLE", "Quoting engine unavailable; try again.")
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
	var manager *settlement.Manager
	if service != nil {
		manager = service.Settlement
	}
	return instrument(mux, manager)
}
