package httpapi

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"rfq-dvp-market.local/backend/internal/durable"
	"rfq-dvp-market.local/backend/internal/failpoint"
	"strings"
	"sync"
	"time"

	"rfq-dvp-market.local/backend/internal/engine"
	"rfq-dvp-market.local/backend/internal/settlement"
	"rfq-dvp-market.local/backend/internal/signing"
)

type SigningService struct {
	readinessMu    sync.RWMutex
	ready          bool
	readinessError string
	Store          *durable.Store

	Settlement *settlement.Manager
	client     *engine.Client
	config     signing.Config
	keys       map[string]*ecdsa.PrivateKey
	now        func() time.Time
}

func NewSigningService(client *engine.Client, config signing.Config) (*SigningService, error) {
	keys, err := signing.DemoKeys(config)
	if err != nil {
		return nil, err
	}
	return &SigningService{client: client, config: config, keys: keys, now: time.Now}, nil
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
func fail(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"code": code, "message": message, "rejections": []any{}})
}
func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		fail(w, 415, "INVALID_REQUEST", "Content-Type application/json is required.")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil || d.Decode(new(any)) != io.EOF {
		fail(w, 400, "INVALID_REQUEST", "Invalid JSON body.")
		return false
	}
	return true
}

func registerRFQs(mux *http.ServeMux, s *SigningService) {
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		if s == nil {
			writeJSON(w, 200, map[string]any{"signing_enabled": false})
			return
		}
		writeJSON(w, 200, map[string]any{"signing_enabled": true, "settlement_enabled": s.Settlement != nil, "deployment": s.config})
	})
	enabled := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s == nil {
				fail(w, 503, "SIGNING_DISABLED", "Start Go with -deployment to enable Anvil signing.")
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /api/readiness", enabled(func(w http.ResponseWriter, r *http.Request) {
		if s.admit(w) {
			writeJSON(w, 200, map[string]string{"status": "ready"})
		}
	}))
	mux.HandleFunc("POST /api/rfqs", enabled(func(w http.ResponseWriter, r *http.Request) {
		if !s.admit(w) {
			return
		}
		var input struct {
			TraderID string `json:"trader_id"`
			Side     string `json:"side"`
			Unit     string `json:"unit"`
			Amount   string `json:"amount"`
		}
		if !decode(w, r, &input) {
			return
		}
		trader, err := signing.Address(input.TraderID)
		if err != nil || (input.Side != "BUY" && input.Side != "SELL") || (input.Unit != "tBILL" && input.Unit != "sEUR") || input.Amount == "" {
			fail(w, 400, "INVALID_REQUEST", "A valid wallet, side, unit and amount are required.")
			return
		}
		for _, id := range []string{"MM1", "MM2", "MM3"} {
			if strings.EqualFold(trader, s.config.Maker(id)) {
				fail(w, 400, "INVALID_TRADER", "Use a wallet distinct from the demo market makers.")
				return
			}
		}
		input.TraderID = trader
		var rfq engine.Rfq
		if !s.call(w, r, "POST", "/rfqs", input, &rfq) {
			return
		}
		writeJSON(w, 201, rfq)
	}))
	mux.HandleFunc("GET /api/rfqs/{id}", enabled(func(w http.ResponseWriter, r *http.Request) {
		var rfq engine.Rfq
		if s.call(w, r, "GET", "/rfqs/"+url.PathEscape(r.PathValue("id")), nil, &rfq) {
			writeJSON(w, 200, rfq)
		}
	}))
	mux.HandleFunc("GET /api/rfqs/{id}/quotes/{quote}/terms", enabled(func(w http.ResponseWriter, r *http.Request) {
		rfq, q, ok := s.load(w, r, r.PathValue("quote"))
		if !ok {
			return
		}
		if rfq.Status != "QUOTED" || uint64(s.now().Unix()) >= rfq.QuoteExpiresAt {
			fail(w, 410, "QUOTE_UNAVAILABLE", "RFQ expired or already accepted.")
			return
		}
		data, err := signing.Typed(s.config, rfq, q)
		if err != nil {
			fail(w, 502, "INVALID_ENGINE_RESPONSE", err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"typed_data": signing.WalletData(data), "quote": q, "quote_expires_at": rfq.QuoteExpiresAt})
	}))
	mux.HandleFunc("POST /api/rfqs/{id}/accept", enabled(s.accept))
	mux.HandleFunc("GET /api/settlements/{trade}", enabled(func(w http.ResponseWriter, r *http.Request) {
		if s.Settlement != nil {
			if result, ok := s.Settlement.Get(r.PathValue("trade")); ok {
				writeJSON(w, 200, result)
				return
			}
		}
		fail(w, 404, "UNKNOWN_SETTLEMENT", "Unknown settlement job.")
	}))
	mux.HandleFunc("GET /api/balances/{account}", enabled(func(w http.ResponseWriter, r *http.Request) {
		if s.Settlement == nil {
			fail(w, 503, "SETTLEMENT_DISABLED", "Enable -rpc to read Anvil balances.")
			return
		}
		if _, err := signing.Address(r.PathValue("account")); err != nil {
			fail(w, 400, "INVALID_ADDRESS", err.Error())
			return
		}
		balances, err := s.Settlement.Balances(r.Context(), r.PathValue("account"))
		if err != nil {
			fail(w, 503, "RPC_UNAVAILABLE", err.Error())
			return
		}
		writeJSON(w, 200, balances)
	}))
}

func (s *SigningService) call(w http.ResponseWriter, r *http.Request, method, path string, input, out any) bool {
	status, body, err := s.client.Call(r.Context(), method, path, input)
	if err != nil {
		fail(w, 503, "ENGINE_UNAVAILABLE", "Engine unavailable. Acceptance may have reserved inventory despite a lost response; keep the signature and retry.")
		return false
	}
	if status != 200 && status != 201 {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_, _ = w.Write(body)
		return false
	}
	if err := json.Unmarshal(body, out); err != nil {
		fail(w, 502, "INVALID_ENGINE_RESPONSE", "Invalid engine response.")
		return false
	}
	return true
}

func (s *SigningService) load(w http.ResponseWriter, r *http.Request, quoteID string) (engine.Rfq, engine.Quote, bool) {
	var rfq engine.Rfq
	if !s.call(w, r, "GET", "/rfqs/"+url.PathEscape(r.PathValue("id")), nil, &rfq) {
		return rfq, engine.Quote{}, false
	}
	if rfq.RfqID != r.PathValue("id") {
		fail(w, 502, "INVALID_ENGINE_RESPONSE", "Inconsistent RFQ.")
		return rfq, engine.Quote{}, false
	}
	for _, q := range rfq.Quotes {
		if q.QuoteID == quoteID {
			return rfq, q, true
		}
	}
	fail(w, 404, "UNKNOWN_QUOTE", "Unknown quote for this RFQ.")
	return rfq, engine.Quote{}, false
}

func checkReservation(r engine.Rfq, q engine.Quote, t engine.Trade) error {
	asset, amount := "tBILL", q.Quantity
	if q.Side == "SELL" {
		asset, amount = "sEUR", q.Cash
	}
	if t.Status != "RESERVED" || t.TradeID != q.TradeID || t.RfqID != r.RfqID || t.TraderID != r.TraderID || t.Quote != q || t.QuoteExpiresAt != r.QuoteExpiresAt || t.SettlementDeadline != r.SettlementDeadline || t.ReservedAsset != asset || t.ReservedAmount != amount || t.AcceptedAt < r.CreatedAt || t.AcceptedAt >= r.QuoteExpiresAt {
		return fmt.Errorf("reservation differs from signed terms")
	}
	return nil
}

type SignedTrade struct {
	Settlement          *settlement.Result `json:"settlement,omitempty"`
	Status              string             `json:"status"`
	SettlementSubmitted bool               `json:"settlement_submitted"`
	Trade               engine.Trade       `json:"trade"`
	TypedData           map[string]any     `json:"typed_data"`
	BuyerSignature      string             `json:"buyer_signature"`
	SellerSignature     string             `json:"seller_signature"`
}

func (s *SigningService) accept(w http.ResponseWriter, r *http.Request) {
	if !s.admit(w) {
		return
	}
	if s.Store != nil {
		if err := s.Store.Check(); err != nil {
			fail(w, 503, "DATABASE_UNAVAILABLE", err.Error())
			return
		}
	}
	var input struct {
		QuoteID   string `json:"quote_id"`
		TradeID   string `json:"trade_id"`
		Signature string `json:"signature"`
	}
	if !decode(w, r, &input) {
		return
	}
	rfq, q, ok := s.load(w, r, input.QuoteID)
	if !ok {
		return
	}
	if input.TradeID != q.TradeID {
		fail(w, 409, "WRONG_TRADE_ID", "The tradeId does not match the quote.")
		return
	}
	data, err := signing.Typed(s.config, rfq, q)
	if err != nil {
		fail(w, 502, "INVALID_ENGINE_RESPONSE", err.Error())
		return
	}
	if err := signing.Verify(data, input.Signature, rfq.TraderID); err != nil {
		fail(w, 403, "INVALID_SIGNATURE", err.Error())
		return
	}
	if s.Settlement != nil {
		if result, ok := s.Settlement.Get(q.TradeID); ok {
			writeJSON(w, 200, map[string]any{"status": result.Status, "settlement": result})
			return
		}
	}
	if uint64(s.now().Unix()) >= rfq.SettlementDeadline {
		fail(w, 410, "SETTLEMENT_DEADLINE", "Settlement deadline reached; no MM signature issued.")
		return
	}
	if s.Store != nil {
		if err := s.Store.Intent(durable.Intent{RfqID: rfq.RfqID, QuoteID: q.QuoteID, TradeID: q.TradeID, Signature: signing.CanonicalSignature(input.Signature)}); err != nil {
			fail(w, 503, "DATABASE_UNAVAILABLE", err.Error())
			return
		}
	}
	var trade engine.Trade
	if rfq.Status == "ACCEPTED" && rfq.Trade != nil {
		trade = *rfq.Trade
	} else {
		if rfq.Status != "QUOTED" {
			fail(w, 410, "QUOTE_EXPIRED", "The quote expired before the signature arrived; request new quotes.")
			return
		}
		payload := map[string]string{"trader_id": rfq.TraderID, "quote_id": q.QuoteID, "trade_id": q.TradeID}
		if !s.call(w, r, "POST", "/rfqs/"+url.PathEscape(rfq.RfqID)+"/accept", payload, &trade) {
			return
		}
	}
	failpoint.Hit("after_reservation")
	if err := checkReservation(rfq, q, trade); err != nil {
		fail(w, 409, "RESERVATION_MISMATCH", err.Error())
		return
	}
	if uint64(s.now().Unix()) >= rfq.SettlementDeadline {
		fail(w, 410, "SETTLEMENT_DEADLINE", "Deadline reached after reservation; inventory remains reserved.")
		return
	}
	mmSignature, err := signing.Sign(data, s.keys[q.MakerID])
	if err != nil {
		fail(w, 500, "SIGNING_FAILED", "MM signing unavailable; inventory remains reserved.")
		return
	}
	traderSignature := signing.CanonicalSignature(input.Signature)
	buyer, seller := traderSignature, mmSignature
	if q.Side == "SELL" {
		buyer, seller = mmSignature, traderSignature
	}
	result := SignedTrade{Status: "SIGNED", Trade: trade, TypedData: signing.WalletData(data), BuyerSignature: buyer, SellerSignature: seller}
	if s.Settlement != nil {
		queued, err := s.Settlement.Enqueue(trade, data, buyer, seller)
		if err != nil {
			fail(w, 500, "SETTLEMENT_QUEUE_FAILED", err.Error())
			return
		}
		result.Settlement = &queued
		slog.Info("trade queued", "rfq_id", rfq.RfqID, "trade_id", trade.TradeID, "maker_id", q.MakerID)
		if s.Store != nil {
			if err := s.Store.Complete(q.TradeID); err != nil {
				fail(w, 503, "DATABASE_UNAVAILABLE", err.Error())
				return
			}
		}
	}
	writeJSON(w, 200, result)
}
