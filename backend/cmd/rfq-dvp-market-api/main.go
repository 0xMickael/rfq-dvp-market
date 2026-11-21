package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"rfq-dvp-market.local/backend/internal/engine"
	"rfq-dvp-market.local/backend/internal/httpapi"
	"rfq-dvp-market.local/backend/internal/settlement"
	"rfq-dvp-market.local/backend/internal/signing"
)

func main() {
	printIdentity := flag.Bool("print-market-id", false, "Print chain/deployment identity without starting services")
	runWorker := flag.Bool("settlement-worker", true, "Process settlement jobs; false keeps the durable queue paused")
	deployment := flag.String("deployment", "", "Anvil deployment JSON; enables publicly known demo MM signers")
	rpcURL := flag.String("rpc", "", "Local Anvil RPC; enables settlement submission and tracking")
	address := flag.String("listen", "127.0.0.1:8080", "Local HTTP listen address")
	engineURL := flag.String("engine", "http://127.0.0.1:8081", "Rust engine URL")
	flag.Parse()
	if os.Getenv("DATABASE_URL") != "" && (*rpcURL == "" || *deployment == "") && !*printIdentity {
		slog.Error("durable mode requires -rpc and -deployment")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	client := engine.NewClient(*engineURL)
	var signer *httpapi.SigningService
	if *deployment != "" {
		raw, err := os.ReadFile(*deployment)
		var config signing.Config
		if err == nil {
			err = json.Unmarshal(raw, &config)
		}
		if err == nil && *printIdentity {
			startup, cancel := context.WithTimeout(ctx, 10*time.Second)
			identity, e := settlement.Identity(startup, *rpcURL, config)
			cancel()
			if e != nil {
				slog.Error("RPC identity unavailable")
				os.Exit(1)
			}
			os.Stdout.WriteString(identity + "\n")
			return
		}
		if err == nil {
			signer, err = httpapi.NewSigningService(client, config)
		}
		if err == nil && *rpcURL != "" {
			startup, cancel := context.WithTimeout(ctx, 10*time.Second)
			signer.Settlement, err = settlement.New(startup, *rpcURL, config, client)
			if err == nil && os.Getenv("DATABASE_URL") != "" {
				identity, e := settlement.Identity(startup, *rpcURL, config)
				if e != nil || identity != os.Getenv("MARKET_ID") {
					err = fmt.Errorf("MARKET_ID does not match the chain or deployment")
				} else {
					err = signer.Settlement.EnablePersistence(startup, os.Getenv("DATABASE_URL"), identity)
				}
				if err == nil {
					signer.Store = signer.Settlement.Store()
				}
			}
			cancel()
			if err == nil {
				defer signer.Settlement.Close()
				if *runWorker {
					go signer.Settlement.Run(ctx)
				}
				if signer.Store != nil {
					go signer.Recover(ctx)
				}
			}
		}
		if err != nil {
			slog.Error("invalid signing configuration", "error", err)
			os.Exit(1)
		}
	}
	if *rpcURL != "" && signer == nil {
		slog.Error("-rpc requires -deployment")
		os.Exit(1)
	}
	server := &http.Server{
		Addr:              *address,
		Handler:           httpapi.Handler(client, signer),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()

	slog.Info("starting rfq-dvp-market API", "address", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("HTTP server stopped", "error", err)
		os.Exit(1)
	}
}
