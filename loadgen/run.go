package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"time"

	"rfq-dvp-market.local/backend/internal/engine"
	"rfq-dvp-market.local/backend/internal/settlement"
	"rfq-dvp-market.local/backend/internal/signing"
)

type operation struct {
	trader       *wallet
	side, amount string
	accept       bool
}
type recordedRFQ struct {
	rfq          engine.Rfq
	quote        engine.Quote
	acknowledged bool
}
type campaign struct {
	client       *client
	measurements measurements
	mu           sync.Mutex
	report       report
	records      []recordedRFQ
	identifiers  map[string]bool
	failures     []string
}

func (c *client) run(ctx context.Context) report {
	run := &campaign{client: c, measurements: measurements{stages: map[string]*stageSamples{}}, identifiers: map[string]bool{}, report: report{Options: c.options}}
	if err := c.call(ctx, "/api/readiness", nil, new(any)); err != nil {
		run.report.Invariants.Errors = []string{err.Error()}
		return run.report
	}
	warmEnd := time.Now().Add(c.options.Warmup)
	for i := 0; time.Now().Before(warmEnd) && ctx.Err() == nil; i++ {
		op := run.plan(-i - 1)
		var rfq engine.Rfq
		if err := c.call(ctx, "/api/rfqs", map[string]string{"trader_id": op.trader.address, "side": op.side, "unit": "tBILL", "amount": op.amount}, &rfq); err != nil {
			run.report.Invariants.Errors = []string{"warmup: " + err.Error()}
			return run.report
		}
		run.report.WarmupRFQs++
	}
	baseline, err := c.baseline(ctx)
	if err != nil {
		run.report.Invariants.Errors = []string{err.Error()}
		return run.report
	}
	started := time.Now()
	run.report.Started = started.UTC()
	admission, cancelAdmission := context.WithTimeout(ctx, c.options.Duration)
	defer cancelAdmission()
	completion, cancelCompletion := context.WithTimeout(ctx, c.options.Duration+c.options.Drain)
	defer cancelCompletion()
	jobs := make(chan operation)
	var workers sync.WaitGroup
	for i := 0; i < c.options.Concurrency; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for op := range jobs {
				op.trader.mu.Lock()
				if completion.Err() == nil {
					run.execute(completion, op)
				}
				op.trader.mu.Unlock()
			}
		}()
	}
dispatch:
	for i := 0; i < c.options.Requests; i++ {
		if admission.Err() != nil {
			break
		}
		select {
		case <-admission.Done():
			break dispatch
		case jobs <- run.plan(i):
		}
	}
	close(jobs)
	run.report.AdmissionSeconds = time.Since(started).Seconds()
	workers.Wait()
	run.report.CompletionSeconds = time.Since(started).Seconds()
	run.report.Cancelled = ctx.Err() != nil || completion.Err() != nil
	run.report.Throughput = float64(run.report.Completed) / run.report.CompletionSeconds
	traders := map[string]bool{}
	for _, record := range run.records {
		traders[record.rfq.TraderID] = true
		if record.quote.Side == "BUY" {
			run.report.BuyRFQs++
		} else {
			run.report.SellRFQs++
		}
	}
	run.report.DistinctTraders = len(traders)
	run.report.Stages = run.measurements.finish()
	// Verification has its own bounded budget and is outside the measurement window.
	audit, cancelAudit := context.WithTimeout(context.Background(), c.options.Drain)
	defer cancelAudit()
	run.report.Invariants = run.verify(audit, baseline)
	return run.report
}
func (run *campaign) plan(index int) operation {
	o := run.client.options
	rng := rand.New(rand.NewSource(o.Seed + int64(index)*104729))
	side := "SELL"
	if rng.Intn(100) < o.BuyPercent {
		side = "BUY"
	}
	low, _ := atoms(o.Min)
	high, _ := atoms(o.Max)
	amount := low.Int64() + rng.Int63n(high.Int64()-low.Int64()+1)
	walletIndex := index % len(run.client.wallets)
	if walletIndex < 0 {
		walletIndex = -walletIndex
	}
	return operation{trader: run.client.wallets[walletIndex], side: side, amount: decimal(amount), accept: o.Mode != "quotes" && rng.Intn(100) < o.AcceptPercent}
}
func (run *campaign) timed(ctx context.Context, stage, path string, input, out any) error {
	started := time.Now()
	err := run.client.call(ctx, path, input, out)
	run.measurements.observe(stage, started, err)
	return err
}
func (run *campaign) execute(ctx context.Context, op operation) {
	started := time.Now()
	run.mu.Lock()
	run.report.Attempted++
	run.mu.Unlock()
	err := run.trade(ctx, op)
	run.measurements.observe("operation", started, err)
	run.mu.Lock()
	defer run.mu.Unlock()
	run.report.Completed++
	if err == nil {
		run.report.Success++
	} else if businessError(err) {
		run.report.Rejected++
	} else {
		run.report.Errors++
	}
}
func (run *campaign) trade(ctx context.Context, op operation) error {
	var rfq engine.Rfq
	if err := run.timed(ctx, "rfq", "/api/rfqs", map[string]string{"trader_id": op.trader.address, "side": op.side, "unit": "tBILL", "amount": op.amount}, &rfq); err != nil {
		return err
	}
	if rfq.RfqID == "" || len(rfq.Quotes) == 0 || rfq.TraderID != op.trader.address {
		return fmt.Errorf("inconsistent RFQ")
	}
	record := recordedRFQ{rfq: rfq, quote: rfq.Quotes[0]}
	run.mu.Lock()
	for _, id := range append([]string{rfq.RfqID}, quoteIDs(rfq)...) {
		if run.identifiers[id] {
			run.failures = append(run.failures, "duplicate identifier: "+id)
		}
		run.identifiers[id] = true
	}
	run.report.Quotes += len(rfq.Quotes)
	position := len(run.records)
	run.records = append(run.records, record)
	if !op.accept {
		run.report.Ignored++
	}
	run.mu.Unlock()
	if !op.accept {
		return nil
	}
	data, err := signing.Typed(run.client.config, rfq, record.quote)
	if err != nil {
		return err
	}
	var prepared struct {
		TypedData map[string]any `json:"typed_data"`
	}
	if err = run.timed(ctx, "terms", "/api/rfqs/"+rfq.RfqID+"/quotes/"+record.quote.QuoteID+"/terms", nil, &prepared); err != nil {
		return err
	}
	expectedJSON, _ := json.Marshal(signing.WalletData(data))
	var expected map[string]any
	_ = json.Unmarshal(expectedJSON, &expected)
	if !reflect.DeepEqual(expected, prepared.TypedData) {
		return fmt.Errorf("server terms differ from the quote")
	}
	signStarted := time.Now()
	signature, err := signing.Sign(data, op.trader.key)
	run.measurements.observe("sign_local", signStarted, err)
	if err != nil {
		return err
	}
	acceptedAt := time.Now()
	var response struct {
		Settlement *settlement.Result `json:"settlement"`
		Buyer      string             `json:"buyer_signature"`
		Seller     string             `json:"seller_signature"`
	}
	err = run.timed(ctx, "accept", "/api/rfqs/"+rfq.RfqID+"/accept", map[string]string{"quote_id": record.quote.QuoteID, "trade_id": record.quote.TradeID, "signature": signature}, &response)
	if err != nil {
		return err
	}
	run.mu.Lock()
	run.records[position].acknowledged = true
	run.mu.Unlock()
	if response.Settlement == nil || response.Settlement.TradeID != record.quote.TradeID {
		return fmt.Errorf("missing or inconsistent settlement job")
	}
	if response.Buyer != "" {
		if err = signing.Verify(data, response.Buyer, data.Message["buyer"].(string)); err != nil {
			return err
		}
		if err = signing.Verify(data, response.Seller, data.Message["seller"].(string)); err != nil {
			return err
		}
	}
	run.mu.Lock()
	run.report.Accepted++
	run.mu.Unlock()
	if run.client.options.Mode == "accept" {
		return nil
	}
	result, err := run.waitSettlement(ctx, record.quote.TradeID, true)
	if err == nil && result.Status != "SETTLED" {
		err = fmt.Errorf("settlement completed with failure")
	}
	run.measurements.observe("settlement_from_accept", acceptedAt, err)
	return err
}
func quoteIDs(rfq engine.Rfq) []string {
	ids := make([]string, 0, len(rfq.Quotes)*2)
	for _, quote := range rfq.Quotes {
		ids = append(ids, quote.QuoteID, quote.TradeID)
	}
	return ids
}
func (run *campaign) waitSettlement(ctx context.Context, id string, measured bool) (settlement.Result, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		var result settlement.Result
		started := time.Now()
		err := run.client.call(ctx, "/api/settlements/"+id, nil, &result)
		if measured {
			run.measurements.observe("poll", started, err)
		}
		if err != nil {
			return result, err
		}
		if result.TradeID != id {
			return result, fmt.Errorf("result belongs to another trade")
		}
		if result.Status == "SETTLED" || result.Status == "SETTLEMENT_FAILED" {
			return result, nil
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-ticker.C:
		}
	}
}
