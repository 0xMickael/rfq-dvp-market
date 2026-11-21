package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"rfq-dvp-market.local/backend/internal/settlement"
	"time"

	"rfq-dvp-market.local/backend/internal/durable"
	"rfq-dvp-market.local/backend/internal/engine"
	"rfq-dvp-market.local/backend/internal/signing"
)

// Recover retries accepted authorizations even when the HTTP client never reconnects.
func (s *SigningService) Recover(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		intents, err := s.Store.Intents()
		if err == nil {
			for _, intent := range intents {
				attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
				err = s.resume(attempt, intent)
				cancel()
				if err != nil {
					slog.Warn("acceptance recovery deferred", "trade_id", intent.TradeID, "error", err)
				}
			}
		}
		if err == nil && len(intents) == 0 {
			attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
			audit := s.Settlement.Reconcile(attempt)
			cancel()
			s.readinessMu.Lock()
			if audit == nil {
				s.ready = true
				s.readinessError = ""
			} else if !errors.Is(audit, settlement.ErrBusy) {
				s.ready = false
				s.readinessError = audit.Error()
			}
			s.readinessMu.Unlock()
		}
	}
}
func (s *SigningService) resume(ctx context.Context, i durable.Intent) error {
	if _, ok := s.Settlement.Get(i.TradeID); ok {
		return s.Store.Complete(i.TradeID)
	}
	code, body, err := s.client.Call(ctx, "GET", "/rfqs/"+url.PathEscape(i.RfqID), nil)
	if err != nil || code != 200 {
		return fmt.Errorf("RFQ unavailable during recovery")
	}
	var rfq engine.Rfq
	if json.Unmarshal(body, &rfq) != nil || rfq.RfqID != i.RfqID {
		return fmt.Errorf("inconsistent recovery RFQ")
	}
	var q engine.Quote
	for _, quote := range rfq.Quotes {
		if quote.QuoteID == i.QuoteID {
			q = quote
			break
		}
	}
	if q.TradeID != i.TradeID {
		return fmt.Errorf("inconsistent recovery quote")
	}
	data, err := signing.Typed(s.config, rfq, q)
	if err != nil {
		return err
	}
	if err = signing.Verify(data, i.Signature, rfq.TraderID); err != nil {
		return err
	}
	if rfq.Trade != nil && rfq.Trade.TradeID != i.TradeID {
		return s.Store.Complete(i.TradeID)
	}
	if rfq.Trade == nil {
		if rfq.Status != "QUOTED" || uint64(s.now().Unix()) >= rfq.QuoteExpiresAt {
			return s.Store.Complete(i.TradeID)
		}
		code, body, err = s.client.Call(ctx, "POST", "/rfqs/"+url.PathEscape(i.RfqID)+"/accept", map[string]string{"trader_id": rfq.TraderID, "quote_id": i.QuoteID, "trade_id": i.TradeID})
		if err != nil || code != 200 {
			return fmt.Errorf("recovery reservation needs reconciliation")
		}
		var trade engine.Trade
		if json.Unmarshal(body, &trade) != nil {
			return fmt.Errorf("unreadable reservation")
		}
		rfq.Trade = &trade
	}
	if err = checkReservation(rfq, q, *rfq.Trade); err != nil {
		return err
	}
	// An expired authorization is kept only so the worker can prove failure on-chain.
	mm, err := signing.Sign(data, s.keys[q.MakerID])
	if err != nil {
		return err
	}
	buyer, seller := signing.CanonicalSignature(i.Signature), mm
	if q.Side == "SELL" {
		buyer, seller = seller, buyer
	}
	if _, err = s.Settlement.Enqueue(*rfq.Trade, data, buyer, seller); err != nil {
		return err
	}
	return s.Store.Complete(i.TradeID)
}

func (s *SigningService) admit(w http.ResponseWriter) bool {
	if s.Store == nil {
		return true
	}
	if err := s.Store.Check(); err != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE", err.Error())
		return false
	}
	s.readinessMu.RLock()
	defer s.readinessMu.RUnlock()
	if !s.ready {
		message := s.readinessError
		if message == "" {
			message = "Recovery and reconciliation in progress."
		}
		fail(w, 503, "RECOVERY_REQUIRED", message)
		return false
	}
	return true
}
