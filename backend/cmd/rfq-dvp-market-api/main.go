package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"rfq-dvp-market.local/backend/internal/httpapi"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	server := &http.Server{
		Addr:              "127.0.0.1:8080",
		Handler:           httpapi.NewHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	slog.Info("démarrage de l'API rfq-dvp-market", "address", server.Addr)
	if err := server.ListenAndServe(); err != nil {
		slog.Error("arrêt du serveur HTTP", "error", err)
		os.Exit(1)
	}
}
