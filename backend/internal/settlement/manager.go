package settlement

import (
	"context"
	"crypto/ecdsa"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"rfq-dvp-market.local/backend/internal/durable"
	"rfq-dvp-market.local/backend/internal/engine"
	"rfq-dvp-market.local/backend/internal/failpoint"
	"rfq-dvp-market.local/backend/internal/signing"
)

//go:embed dvp.json
var contractJSON string
var contractABI = mustABI(contractJSON)
var tokenABI = mustABI(`[{"type":"function","name":"balanceOf","inputs":[{"name":"account","type":"address"}],"outputs":[{"type":"uint256"}]},{"type":"function","name":"decimals","inputs":[],"outputs":[{"type":"uint8"}]}]`)

func mustABI(raw string) abi.ABI {
	a, err := abi.JSON(strings.NewReader(raw))
	if err != nil {
		panic(err)
	}
	return a
}

type Terms struct {
	TradeId                                 [32]byte
	Buyer, Seller, CashToken, SecurityToken common.Address
	CashAmount, SecurityAmount, Deadline    *big.Int
}

func terms(data apitypes.TypedData) Terms {
	m := data.Message
	integer := func(name string) *big.Int { n, _ := new(big.Int).SetString(m[name].(string), 10); return n }
	return Terms{common.HexToHash(m["tradeId"].(string)), common.HexToAddress(m["buyer"].(string)), common.HexToAddress(m["seller"].(string)), common.HexToAddress(m["cashToken"].(string)), common.HexToAddress(m["securityToken"].(string)), integer("cashAmount"), integer("securityAmount"), integer("deadline")}
}

type Result struct {
	TradeID         string `json:"trade_id"`
	Status          string `json:"status"`
	TransactionHash string `json:"transaction_hash,omitempty"`
	Message         string `json:"message"`
}
type job struct {
	observed *observation
	trade    engine.Trade
	terms    Terms
	data     []byte
	tx       *types.Transaction
	result   Result
}

// One worker assigns nonces; mu protects jobs and workMu serializes processing and reconciliation.
type Manager struct {
	attempts   atomic.Uint64
	retries    atomic.Uint64
	workMu     sync.Mutex
	store      *durable.Store
	mu         sync.Mutex
	jobs       map[string]*job
	rpc        *ethclient.Client
	engine     *engine.Client
	config     signing.Config
	key        *ecdsa.PrivateKey
	from       common.Address
	nonce      uint64
	nonceReady bool
}

func New(ctx context.Context, endpoint string, c signing.Config, e *engine.Client) (*Manager, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
		return nil, fmt.Errorf("local Anvil HTTP RPC required")
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	rpc, err := ethclient.DialContext(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	// Public Anvil account index 5 pays for local settlement gas.
	key, _ := crypto.HexToECDSA("8b3a350cf5c34c9194ca85829a2df0ec3153be0318b5e2d3348e872092edffba")
	m := &Manager{rpc: rpc, engine: e, config: c, key: key, from: crypto.PubkeyToAddress(key.PublicKey), jobs: map[string]*job{}}
	if err := m.validate(ctx); err != nil {
		rpc.Close()
		return nil, err
	}
	return m, nil
}
func (m *Manager) Close() {
	m.rpc.Close()
	if m.store != nil {
		m.store.Close()
	}
}
func (m *Manager) validate(ctx context.Context) error {
	id, err := m.rpc.ChainID(ctx)
	if err != nil {
		return err
	}
	if id.Cmp(big.NewInt(31337)) != 0 {
		return fmt.Errorf("RPC chain is not Anvil 31337")
	}
	for _, address := range []string{m.config.Settlement, m.config.CashToken, m.config.SecurityToken} {
		code, err := m.rpc.CodeAt(ctx, common.HexToAddress(address), nil)
		if err != nil {
			return err
		}
		if len(code) == 0 {
			return fmt.Errorf("contract missing: %s", address)
		}
	}
	for name, expected := range map[string]string{"cashToken": m.config.CashToken, "securityToken": m.config.SecurityToken} {
		output, err := m.call(ctx, contractABI, common.HexToAddress(m.config.Settlement), name, nil)
		if err != nil {
			return err
		}
		if output[0].(common.Address) != common.HexToAddress(expected) {
			return fmt.Errorf("incorrect DvP token pair")
		}
	}
	for _, address := range []string{m.config.CashToken, m.config.SecurityToken} {
		output, err := m.call(ctx, tokenABI, common.HexToAddress(address), "decimals", nil)
		if err != nil {
			return err
		}
		if output[0].(uint8) != 6 {
			return fmt.Errorf("six decimal places required")
		}
	}
	return nil
}
func (m *Manager) call(ctx context.Context, a abi.ABI, to common.Address, name string, block *big.Int, args ...any) ([]any, error) {
	data, err := a.Pack(name, args...)
	if err != nil {
		return nil, err
	}
	raw, err := m.rpc.CallContract(ctx, ethereum.CallMsg{To: &to, Data: data}, block)
	if err != nil {
		return nil, err
	}
	return a.Unpack(name, raw)
}
func (m *Manager) Get(id string) (Result, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return Result{}, false
	}
	return j.result, true
}
func (m *Manager) set(j *job, status, hash, message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j.result.Status = status
	j.result.TransactionHash = hash
	j.result.Message = message
	return m.save(j)
}
func (m *Manager) Enqueue(t engine.Trade, data apitypes.TypedData, buyer, seller string) (Result, error) {
	m.workMu.Lock()
	defer m.workMu.Unlock()
	if err := signing.Verify(data, buyer, data.Message["buyer"].(string)); err != nil {
		return Result{}, err
	}
	if err := signing.Verify(data, seller, data.Message["seller"].(string)); err != nil {
		return Result{}, err
	}
	value := terms(data)
	encoded, err := contractABI.Pack("settle", value, common.FromHex(buyer), common.FromHex(seller))
	if err != nil {
		return Result{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.jobs[t.TradeID]; ok {
		return existing.result, nil
	}
	result := Result{TradeID: t.TradeID, Status: "SETTLEMENT_PENDING", Message: "Signatures verified; settlement pending."}
	j := &job{trade: t, terms: value, data: encoded, result: result}
	if err := m.save(j); err != nil {
		return Result{}, err
	}
	m.jobs[t.TradeID] = j
	return result, nil
}
func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.mu.Lock()
			jobs := make([]*job, 0, len(m.jobs))
			for _, j := range m.jobs {
				if j.result.Status == "SETTLEMENT_PENDING" {
					jobs = append(jobs, j)
				}
			}
			m.mu.Unlock()
			for _, j := range jobs {
				m.attempts.Add(1)
				attempt, cancel := context.WithTimeout(ctx, 4*time.Second)
				err := m.step(attempt, j)
				cancel()
				if err != nil {
					m.retries.Add(1)
					slog.Warn("settlement retry", "trade_id", j.trade.TradeID, "rfq_id", j.trade.RfqID, "error", err)
					hash := ""
					if j.tx != nil {
						hash = j.tx.Hash().Hex()
					}
					m.set(j, "SETTLEMENT_PENDING", hash, err.Error()+"; inventory remains reserved, tracking continues automatically.")
				}
			}
		}
	}
}

func (m *Manager) Metrics() (pending, settled, failed int, attempts, retries uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		switch j.result.Status {
		case "SETTLEMENT_PENDING":
			pending++
		case "SETTLED":
			settled++
		case "SETTLEMENT_FAILED":
			failed++
		}
	}
	return pending, settled, failed, m.attempts.Load(), m.retries.Load()
}
func (m *Manager) notify(ctx context.Context, j *job, status, hash string, timestamp uint64) error {
	var txHash any
	if hash != "" {
		txHash = hash
	}
	payload := map[string]any{"trade_id": j.trade.TradeID, "status": status, "transaction_hash": txHash, "block_timestamp": timestamp}
	code, body, err := m.engine.Call(ctx, "POST", "/rfqs/"+url.PathEscape(j.trade.RfqID)+"/settlement", payload)
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("Rust notification rejected (%d)", code)
	}
	var trade engine.Trade
	if json.Unmarshal(body, &trade) != nil || trade.TradeID != j.trade.TradeID || trade.Status != status {
		return fmt.Errorf("inconsistent Rust notification")
	}
	return nil
}

func (m *Manager) step(ctx context.Context, j *job) error {
	m.workMu.Lock()
	defer m.workMu.Unlock()
	if m.store != nil {
		if err := m.store.Check(); err != nil {
			return err
		}
	}
	if j.observed != nil {
		return m.finish(ctx, j)
	}
	header, err := m.rpc.HeaderByNumber(ctx, nil)
	if err != nil {
		return err
	}
	to := common.HexToAddress(m.config.Settlement)
	state, err := m.call(ctx, contractABI, to, "settled", header.Number, j.terms.Buyer, j.terms.Seller, j.terms.TradeId)
	if err != nil {
		return err
	}
	// Another party may also submit the authorized signatures.
	if state[0].(bool) {
		event := contractABI.Events["TradeSettled"]
		logs, err := m.rpc.FilterLogs(ctx, ethereum.FilterQuery{FromBlock: big.NewInt(0), ToBlock: header.Number, Addresses: []common.Address{to}, Topics: [][]common.Hash{{event.ID}, {common.Hash(j.terms.TradeId)}, {common.BytesToHash(j.terms.Buyer.Bytes())}, {common.BytesToHash(j.terms.Seller.Bytes())}}})
		if err != nil {
			return err
		}
		for _, log := range logs {
			values, err := event.Inputs.NonIndexed().Unpack(log.Data)
			if err != nil {
				return err
			}
			if values[0].(*big.Int).Cmp(j.terms.CashAmount) != 0 || values[1].(*big.Int).Cmp(j.terms.SecurityAmount) != 0 {
				return fmt.Errorf("settlement event amounts do not match")
			}
			receipt, err := m.rpc.TransactionReceipt(ctx, log.TxHash)
			if err != nil {
				return err
			}
			block, err := m.rpc.HeaderByNumber(ctx, new(big.Int).SetUint64(log.BlockNumber))
			if err != nil {
				return err
			}
			if receipt.Status != types.ReceiptStatusSuccessful || receipt.BlockHash != block.Hash() || log.BlockHash != block.Hash() || log.Removed {
				return fmt.Errorf("noncanonical settlement evidence")
			}
			j.observed = &observation{Status: "SETTLED", Hash: log.TxHash.Hex(), Timestamp: block.Time}
			if err := m.save(j); err != nil {
				return err
			}
			failpoint.Hit("after_inclusion")
			return m.finish(ctx, j)
		}
		return fmt.Errorf("trade consumed without a verifiable event")
	}
	if header.Time >= j.trade.SettlementDeadline {
		if j.tx != nil {
			// Never abandon a signed nonce: subsequent transactions would remain
			// blocked behind it. Even an expired transaction can be included
			// with a revert, without moving any tokens.
			_, err := m.rpc.TransactionReceipt(ctx, j.tx.Hash())
			if err == ethereum.NotFound {
				if err := m.rpc.SendTransaction(ctx, j.tx); err != nil {
					return err
				}
				return fmt.Errorf("deadline reached; waiting for submitter nonce consumption")
			}
			if err != nil {
				return err
			}
		}
		current, err := m.rpc.HeaderByNumber(ctx, header.Number)
		if err != nil {
			return err
		}
		if current.Hash() != header.Hash() {
			return fmt.Errorf("block changed during verification")
		}
		hash := ""
		if j.tx != nil {
			hash = j.tx.Hash().Hex()
		}
		j.observed = &observation{Status: "SETTLEMENT_FAILED", Hash: hash, Timestamp: header.Time}
		if err := m.save(j); err != nil {
			return err
		}
		return m.finish(ctx, j)
	}
	if j.tx == nil {
		if err := m.notify(ctx, j, "SETTLEMENT_PENDING", "", 0); err != nil {
			return err
		}
		output, err := m.call(ctx, contractABI, to, "hashTrade", nil, j.terms)
		if err != nil {
			return err
		}
		data, err := signing.Typed(m.config, engine.Rfq{TraderID: j.trade.TraderID, QuoteExpiresAt: j.trade.QuoteExpiresAt, SettlementDeadline: j.trade.SettlementDeadline}, j.trade.Quote)
		if err != nil {
			return err
		}
		digest, err := signing.Digest(data)
		if err != nil {
			return err
		}
		if common.Hash(output[0].([32]byte)) != common.BytesToHash(digest) {
			return fmt.Errorf("incorrect contract EIP-712 domain")
		}
		gas, err := m.rpc.EstimateGas(ctx, ethereum.CallMsg{From: m.from, To: &to, Data: j.data})
		if err != nil {
			return fmt.Errorf("simulation rejected (balances, allowances or deadline): %w", err)
		}
		price, err := m.rpc.SuggestGasPrice(ctx)
		if err != nil {
			return err
		}
		networkNonce, err := m.rpc.PendingNonceAt(ctx, m.from)
		if err != nil {
			return err
		}
		if !m.nonceReady || networkNonce > m.nonce {
			m.nonce = networkNonce
			m.nonceReady = true
		}
		tx, err := types.SignTx(types.NewTx(&types.LegacyTx{Nonce: m.nonce, To: &to, Gas: gas + gas/5, GasPrice: price, Data: j.data}), types.LatestSignerForChainID(big.NewInt(31337)), m.key)
		if err != nil {
			return err
		}
		j.tx = tx
		if err := m.save(j); err != nil {
			return err
		}
		failpoint.Hit("after_sign")
		m.nonce++
	}
	hash := j.tx.Hash().Hex()
	if err := m.notify(ctx, j, "SETTLEMENT_PENDING", hash, 0); err != nil {
		return err
	}
	receipt, err := m.rpc.TransactionReceipt(ctx, j.tx.Hash())
	if err == nil {
		if receipt.Status == types.ReceiptStatusFailed {
			return fmt.Errorf("transaction reverted; waiting for resolution at the deadline")
		}
		return fmt.Errorf("receipt received; verifying settlement on the next pass")
	}
	if err != ethereum.NotFound {
		return err
	}
	// A lost response reuses the same transaction bytes and nonce.
	if err := m.rpc.SendTransaction(ctx, j.tx); err != nil {
		return fmt.Errorf("RPC submission needs verification: %w", err)
	}
	failpoint.Hit("after_send")
	return m.set(j, "SETTLEMENT_PENDING", hash, "Transaction submitted; awaiting confirmation.")
}
func (m *Manager) Balances(ctx context.Context, account string) (map[string]string, error) {
	address, err := signing.Address(account)
	if err != nil {
		return nil, err
	}
	balances := map[string]string{}
	for name, token := range map[string]string{"sEUR": m.config.CashToken, "tBILL": m.config.SecurityToken} {
		out, err := m.call(ctx, tokenABI, common.HexToAddress(token), "balanceOf", nil, common.HexToAddress(address))
		if err != nil {
			return nil, err
		}
		n := out[0].(*big.Int)
		whole, fraction := new(big.Int), new(big.Int)
		whole.QuoRem(n, big.NewInt(1000000), fraction)
		balances[name] = fmt.Sprintf("%s.%06d", whole.String(), fraction.Uint64())
	}
	return balances, nil
}

func (m *Manager) finish(ctx context.Context, j *job) error {
	o := j.observed
	if err := m.notify(ctx, j, o.Status, o.Hash, o.Timestamp); err != nil {
		return err
	}
	if err := m.set(j, o.Status, o.Hash, "On-chain result verified; inventory accounting completed."); err != nil {
		return err
	}
	slog.Info("settlement completed", "rfq_id", j.trade.RfqID, "trade_id", j.trade.TradeID, "status", o.Status, "transaction_hash", o.Hash)
	return nil
}
