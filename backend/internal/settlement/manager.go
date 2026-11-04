package settlement

import (
	"context"
	"crypto/ecdsa"
	_ "embed"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"rfq-dvp-market.local/backend/internal/engine"
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
	trade  engine.Trade
	terms  Terms
	data   []byte
	tx     *types.Transaction
	result Result
}

// Un seul worker attribue les nonces. Aucun appel réseau sous le mutex.
type Manager struct {
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
		return nil, fmt.Errorf("RPC HTTP local Anvil requis")
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	rpc, err := ethclient.DialContext(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	// Compte public Anvil index 5, réservé au gas de cette démo.
	key, _ := crypto.HexToECDSA("8b3a350cf5c34c9194ca85829a2df0ec3153be0318b5e2d3348e872092edffba")
	m := &Manager{rpc: rpc, engine: e, config: c, key: key, from: crypto.PubkeyToAddress(key.PublicKey), jobs: map[string]*job{}}
	if err := m.validate(ctx); err != nil {
		rpc.Close()
		return nil, err
	}
	return m, nil
}
func (m *Manager) Close() { m.rpc.Close() }
func (m *Manager) validate(ctx context.Context) error {
	id, err := m.rpc.ChainID(ctx)
	if err != nil {
		return err
	}
	if id.Cmp(big.NewInt(31337)) != 0 {
		return fmt.Errorf("chaîne RPC différente d’Anvil 31337")
	}
	for _, address := range []string{m.config.Settlement, m.config.CashToken, m.config.SecurityToken} {
		code, err := m.rpc.CodeAt(ctx, common.HexToAddress(address), nil)
		if err != nil {
			return err
		}
		if len(code) == 0 {
			return fmt.Errorf("contrat absent : %s", address)
		}
	}
	for name, expected := range map[string]string{"cashToken": m.config.CashToken, "securityToken": m.config.SecurityToken} {
		output, err := m.call(ctx, contractABI, common.HexToAddress(m.config.Settlement), name, nil)
		if err != nil {
			return err
		}
		if output[0].(common.Address) != common.HexToAddress(expected) {
			return fmt.Errorf("paire DvP incorrecte")
		}
	}
	for _, address := range []string{m.config.CashToken, m.config.SecurityToken} {
		output, err := m.call(ctx, tokenABI, common.HexToAddress(address), "decimals", nil)
		if err != nil {
			return err
		}
		if output[0].(uint8) != 6 {
			return fmt.Errorf("six décimales requises")
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
func (m *Manager) set(j *job, status, hash, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j.result.Status = status
	j.result.TransactionHash = hash
	j.result.Message = message
}
func (m *Manager) Enqueue(t engine.Trade, data apitypes.TypedData, buyer, seller string) (Result, error) {
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
	result := Result{TradeID: t.TradeID, Status: "SETTLEMENT_PENDING", Message: "Signatures vérifiées ; settlement en attente."}
	m.jobs[t.TradeID] = &job{trade: t, terms: value, data: encoded, result: result}
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
				attempt, cancel := context.WithTimeout(ctx, 4*time.Second)
				err := m.step(attempt, j)
				cancel()
				if err != nil {
					hash := ""
					if j.tx != nil {
						hash = j.tx.Hash().Hex()
					}
					m.set(j, "SETTLEMENT_PENDING", hash, err.Error()+" ; réservation conservée, suivi automatique.")
				}
			}
		}
	}
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
		return fmt.Errorf("notification Rust refusée (%d)", code)
	}
	var trade engine.Trade
	if json.Unmarshal(body, &trade) != nil || trade.TradeID != j.trade.TradeID || trade.Status != status {
		return fmt.Errorf("notification Rust incohérente")
	}
	return nil
}

func (m *Manager) step(ctx context.Context, j *job) error {
	header, err := m.rpc.HeaderByNumber(ctx, nil)
	if err != nil {
		return err
	}
	to := common.HexToAddress(m.config.Settlement)
	state, err := m.call(ctx, contractABI, to, "settled", header.Number, j.terms.Buyer, j.terms.Seller, j.terms.TradeId)
	if err != nil {
		return err
	}
	// Les signatures peuvent aussi être soumises par une autre partie.
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
				return fmt.Errorf("événement settlement avec montants différents")
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
				return fmt.Errorf("preuve non canonique")
			}
			if err := m.notify(ctx, j, "SETTLED", log.TxHash.Hex(), block.Time); err != nil {
				return err
			}
			m.set(j, "SETTLED", log.TxHash.Hex(), "Échange confirmé sur Anvil ; inventaire comptabilisé.")
			return nil
		}
		return fmt.Errorf("trade consommé sans événement vérifiable")
	}
	if header.Time >= j.trade.SettlementDeadline {
		if j.tx != nil {
			// Un nonce signé ne doit pas être abandonné : les transactions suivantes
			// resteraient bloquées derrière lui. Même expirée, cette transaction
			// peut être incluse avec un revert, sans déplacer de tokens.
			_, err := m.rpc.TransactionReceipt(ctx, j.tx.Hash())
			if err == ethereum.NotFound {
				if err := m.rpc.SendTransaction(ctx, j.tx); err != nil {
					return err
				}
				return fmt.Errorf("deadline atteinte ; attente de consommation du nonce submitter")
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
			return fmt.Errorf("bloc modifié pendant la vérification")
		}
		hash := ""
		if j.tx != nil {
			hash = j.tx.Hash().Hex()
		}
		if err := m.notify(ctx, j, "SETTLEMENT_FAILED", hash, header.Time); err != nil {
			return err
		}
		m.set(j, "SETTLEMENT_FAILED", hash, "Deadline atteinte sur Anvil sans règlement ; réservation libérée.")
		return nil
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
			return fmt.Errorf("domaine EIP-712 du contrat incorrect")
		}
		gas, err := m.rpc.EstimateGas(ctx, ethereum.CallMsg{From: m.from, To: &to, Data: j.data})
		if err != nil {
			return fmt.Errorf("simulation refusée (soldes, allowances ou deadline) : %w", err)
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
		m.nonce++
	}
	hash := j.tx.Hash().Hex()
	if err := m.notify(ctx, j, "SETTLEMENT_PENDING", hash, 0); err != nil {
		return err
	}
	receipt, err := m.rpc.TransactionReceipt(ctx, j.tx.Hash())
	if err == nil {
		if receipt.Status == types.ReceiptStatusFailed {
			return fmt.Errorf("transaction revertée ; attente de résolution à la deadline")
		}
		return fmt.Errorf("receipt reçu, vérification du settlement au prochain passage")
	}
	if err != ethereum.NotFound {
		return err
	}
	// Une réponse perdue ne crée pas une nouvelle transaction : mêmes octets et nonce.
	if err := m.rpc.SendTransaction(ctx, j.tx); err != nil {
		return fmt.Errorf("soumission RPC à vérifier : %w", err)
	}
	m.set(j, "SETTLEMENT_PENDING", hash, "Transaction soumise ; attente de confirmation.")
	return nil
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
