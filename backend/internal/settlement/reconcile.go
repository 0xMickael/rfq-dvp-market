package settlement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"rfq-dvp-market.local/backend/internal/engine"
)

var ErrBusy = errors.New("job recovery in progress")

// Reconcile compares booked balances only at a quiescent boundary, at one RPC block.
// It detects divergence; it never mints, resets inventory or releases an orphan.
func (m *Manager) Reconcile(ctx context.Context) error {
	m.workMu.Lock()
	defer m.workMu.Unlock()
	if err := m.store.Check(); err != nil {
		return err
	}
	code, body, err := m.engine.Call(ctx, "GET", "/trades/pending", nil)
	if err != nil || code != 200 {
		return fmt.Errorf("reconciliation: engine unavailable")
	}
	var pending []engine.Trade
	if json.Unmarshal(body, &pending) != nil {
		return fmt.Errorf("reconciliation: invalid response")
	}
	m.mu.Lock()
	for _, trade := range pending {
		if _, ok := m.jobs[trade.TradeID]; !ok {
			m.mu.Unlock()
			// An HTTP acceptance can be between reservation and enqueue while we audit.
			intents, err := m.store.Intents()
			if err != nil {
				return err
			}
			for _, intent := range intents {
				if intent.TradeID == trade.TradeID {
					return ErrBusy
				}
			}
			return fmt.Errorf("orphan reservation %s: intervention required, no automatic reset", trade.TradeID)
		}
	}
	busy := len(pending) > 0
	for _, j := range m.jobs {
		if j.result.Status == "SETTLEMENT_PENDING" {
			busy = true
		}
	}
	m.mu.Unlock()
	if busy {
		return ErrBusy
	}
	code, body, err = m.engine.Call(ctx, "GET", "/market", nil)
	if err != nil || code != 200 {
		return fmt.Errorf("reconciliation: snapshot unavailable")
	}
	var market struct {
		Makers []struct{ ID, Tbill, Cash string }
	}
	if json.Unmarshal(body, &market) != nil || len(market.Makers) != 3 {
		return fmt.Errorf("reconciliation: invalid snapshot")
	}
	header, err := m.rpc.HeaderByNumber(ctx, nil)
	if err != nil {
		return fmt.Errorf("reconciliation: RPC unavailable")
	}
	for _, maker := range market.Makers {
		address := common.HexToAddress(m.config.Maker(maker.ID))
		for _, asset := range []struct{ token, amount string }{{m.config.CashToken, maker.Cash}, {m.config.SecurityToken, maker.Tbill}} {
			expected, ok := new(big.Int).SetString(strings.ReplaceAll(asset.amount, ".", ""), 10)
			if !ok {
				return fmt.Errorf("reconciliation: invalid amount")
			}
			out, err := m.call(ctx, tokenABI, common.HexToAddress(asset.token), "balanceOf", header.Number, address)
			if err != nil {
				return fmt.Errorf("reconciliation: RPC unavailable")
			}
			if out[0].(*big.Int).Cmp(expected) != 0 {
				return fmt.Errorf("on-chain balance mismatch for %s: new acceptances suspended, no automatic reset", maker.ID)
			}
		}
	}
	current, err := m.rpc.HeaderByNumber(ctx, header.Number)
	if err != nil || current.Hash() != header.Hash() {
		return fmt.Errorf("reconciliation: block changed")
	}
	return nil
}
