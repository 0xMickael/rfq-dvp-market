package httpapi

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"rfq-dvp-market.local/backend/internal/engine"
)

func NewHandler() http.Handler { return Handler(engine.NewClient("http://127.0.0.1:8081")) }

func Handler(client *engine.Client) http.Handler {
	mux := http.NewServeMux()
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
			fail(415, "INVALID_REQUEST", "Content-Type application/json requis.")
			return
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		var input engine.Request
		if err := decoder.Decode(&input); err != nil {
			fail(400, "INVALID_REQUEST", "Corps JSON invalide.")
			return
		}
		if decoder.Decode(new(any)) != io.EOF || (input.Side != "BUY" && input.Side != "SELL") || (input.Unit != "tBILL" && input.Unit != "sEUR") || input.Amount == "" {
			fail(400, "INVALID_REQUEST", "side BUY/SELL, unit tBILL/sEUR et amount en chaîne requis.")
			return
		}
		status, body, err := client.Quote(r.Context(), input)
		if err != nil {
			fail(503, "ENGINE_UNAVAILABLE", "Moteur de cotation indisponible. Réessayez.")
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
	return mux
}
