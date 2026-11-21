package settlement

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"rfq-dvp-market.local/backend/internal/durable"
	"rfq-dvp-market.local/backend/internal/engine"
	"rfq-dvp-market.local/backend/internal/signing"
)

type observation struct {
	Status, Hash string
	Timestamp    uint64
}
type savedJob struct {
	Trade    engine.Trade
	Data     []byte
	Result   Result
	Observed *observation
}

// Identity binds a database to contracts AND chain history, not just chainId 31337.
func Identity(ctx context.Context, endpoint string, c signing.Config) (string, error) {
	rpc, err := ethclient.DialContext(ctx, endpoint)
	if err != nil {
		return "", err
	}
	defer rpc.Close()
	genesis, err := rpc.HeaderByNumber(ctx, big.NewInt(0))
	if err != nil {
		return "", err
	}
	deployment, err := rpc.HeaderByNumber(ctx, big.NewInt(1))
	if err != nil {
		return "", err
	}
	config, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return crypto.Keccak256Hash(config, genesis.Hash().Bytes(), deployment.Hash().Bytes()).Hex(), nil
}

func (m *Manager) EnablePersistence(ctx context.Context, url, identity string) error {
	store, err := durable.Open(ctx, url, identity)
	if err != nil {
		return err
	}
	jobs, err := store.Jobs()
	if err != nil {
		store.Close()
		return err
	}
	for _, value := range jobs {
		var saved savedJob
		if err = json.Unmarshal(value.Payload, &saved); err != nil {
			store.Close()
			return fmt.Errorf("invalid durable job")
		}
		data, err := signing.Typed(m.config, engine.Rfq{TraderID: saved.Trade.TraderID, QuoteExpiresAt: saved.Trade.QuoteExpiresAt, SettlementDeadline: saved.Trade.SettlementDeadline}, saved.Trade.Quote)
		if err != nil {
			store.Close()
			return err
		}
		j := &job{trade: saved.Trade, terms: terms(data), data: saved.Data, result: saved.Result, observed: saved.Observed}
		if value.Raw != nil {
			tx := new(types.Transaction)
			if err = tx.UnmarshalBinary(value.Raw); err != nil || tx.Hash().Hex() != value.Hash || value.Nonce == nil || tx.Nonce() != uint64(*value.Nonce) {
				store.Close()
				return fmt.Errorf("inconsistent durable transaction")
			}
			j.tx = tx
			sender, senderErr := types.Sender(types.LatestSignerForChainID(big.NewInt(m.config.ChainID)), tx)
			if senderErr != nil || sender != m.from || tx.To() == nil || *tx.To() != common.HexToAddress(m.config.Settlement) || !bytes.Equal(tx.Data(), j.data) || tx.ChainId().Cmp(big.NewInt(m.config.ChainID)) != 0 {
				store.Close()
				return fmt.Errorf("durable transaction differs from the authorized job")
			}
			if tx.Nonce() >= m.nonce {
				m.nonce = tx.Nonce() + 1
				m.nonceReady = true
			}
		}
		m.jobs[value.ID] = j
	}
	m.store = store
	return nil
}
func (m *Manager) Store() *durable.Store { return m.store }
func (m *Manager) save(j *job) error {
	if m.store == nil {
		return nil
	}
	payload, err := json.Marshal(savedJob{Trade: j.trade, Data: j.data, Result: j.result, Observed: j.observed})
	if err != nil {
		return err
	}
	value := durable.Job{ID: j.trade.TradeID, Payload: payload}
	if j.tx != nil {
		value.Raw, err = j.tx.MarshalBinary()
		if err != nil {
			return err
		}
		nonce := int64(j.tx.Nonce())
		if nonce < 0 {
			return fmt.Errorf("nonce out of range")
		}
		value.Nonce = &nonce
		value.Hash = j.tx.Hash().Hex()
	}
	return m.store.Save(value)
}
