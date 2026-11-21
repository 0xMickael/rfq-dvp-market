package settlement

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
	"rfq-dvp-market.local/backend/internal/engine"
	"rfq-dvp-market.local/backend/internal/signing"
)

type fakeEth struct {
	header        *types.Header
	chain         hexutil.Big
	config        signing.Config
	consumed      bool
	receipt       *types.Receipt
	logs          []types.Log
	sent          []*types.Transaction
	loseSend      bool
	loseAfterSend bool
	wrongAmounts  bool
}

func abiTerms(value any) *Terms                                              { return abi.ConvertType(value, new(Terms)).(*Terms) }
func (f *fakeEth) ChainId() *hexutil.Big                                     { return &f.chain }
func (f *fakeEth) GetCode(common.Address, string) hexutil.Bytes              { return []byte{1} }
func (f *fakeEth) GetBlockByNumber(string, bool) *types.Header               { return f.header }
func (f *fakeEth) GasPrice() *hexutil.Big                                    { return (*hexutil.Big)(big.NewInt(1)) }
func (f *fakeEth) EstimateGas(map[string]any) hexutil.Uint64                 { return 200000 }
func (f *fakeEth) GetTransactionCount(common.Address, string) hexutil.Uint64 { return 0 }
func (f *fakeEth) GetTransactionReceipt(common.Hash) any {
	if f.receipt == nil {
		return nil
	}
	return f.receipt
}
func (f *fakeEth) GetLogs(map[string]any) []types.Log { return f.logs }
func (f *fakeEth) Call(args map[string]any, _ string) (hexutil.Bytes, error) {
	raw, _ := args["input"].(string)
	if raw == "" {
		raw, _ = args["data"].(string)
	}
	input := common.FromHex(raw)
	if len(input) < 4 {
		return nil, errors.New("bad call")
	}
	if method, err := tokenABI.MethodById(input[:4]); err == nil {
		return method.Outputs.Pack(uint8(6))
	}
	method, err := contractABI.MethodById(input[:4])
	if err != nil {
		return nil, err
	}
	switch method.Name {
	case "cashToken":
		return method.Outputs.Pack(common.HexToAddress(f.config.CashToken))
	case "securityToken":
		return method.Outputs.Pack(common.HexToAddress(f.config.SecurityToken))
	case "settled":
		return method.Outputs.Pack(f.consumed)
	case "hashTrade":
		values, err := method.Inputs.Unpack(input[4:])
		if err != nil {
			return nil, err
		}
		value := *abiTerms(values[0])
		// Compare the fixture digest with the hash expected by the worker.
		data, err := signing.Typed(f.config, engine.Rfq{TraderID: value.Buyer.Hex(), QuoteExpiresAt: 120, SettlementDeadline: value.Deadline.Uint64()}, engine.Quote{TradeID: common.Hash(value.TradeId).Hex(), MakerID: "MM2", Side: "BUY", Cash: "98.000000", Quantity: "1.000000"})
		if err != nil {
			return nil, err
		}
		hash, err := signing.Digest(data)
		if err != nil {
			return nil, err
		}
		return method.Outputs.Pack(common.BytesToHash(hash))
	}
	return nil, errors.New("unexpected method")
}
func (f *fakeEth) SendRawTransaction(raw hexutil.Bytes) (common.Hash, error) {
	tx := new(types.Transaction)
	if err := tx.UnmarshalBinary(raw); err != nil {
		return common.Hash{}, err
	}
	f.sent = append(f.sent, tx)
	if f.loseSend && len(f.sent) == 1 {
		return common.Hash{}, errors.New("RPC response lost")
	}
	args, err := contractABI.Methods["settle"].Inputs.Unpack(tx.Data()[4:])
	if err != nil {
		return common.Hash{}, err
	}
	value := abiTerms(args[0])
	if f.header.Time >= value.Deadline.Uint64() {
		f.receipt = &types.Receipt{Status: 0, CumulativeGasUsed: 30000, GasUsed: 30000, Logs: []*types.Log{}, TxHash: tx.Hash(), BlockNumber: f.header.Number, BlockHash: f.header.Hash()}
		return tx.Hash(), nil
	}
	amount := value.CashAmount
	if f.wrongAmounts {
		amount = big.NewInt(1)
	}
	event := contractABI.Events["TradeSettled"]
	body, _ := event.Inputs.NonIndexed().Pack(amount, value.SecurityAmount)
	f.logs = []types.Log{{Address: common.HexToAddress(f.config.Settlement), Topics: []common.Hash{event.ID, common.Hash(value.TradeId), common.BytesToHash(value.Buyer.Bytes()), common.BytesToHash(value.Seller.Bytes())}, Data: body, BlockNumber: f.header.Number.Uint64(), BlockHash: f.header.Hash(), TxHash: tx.Hash()}}
	f.receipt = &types.Receipt{Status: 1, CumulativeGasUsed: 100000, GasUsed: 100000, Logs: []*types.Log{&f.logs[0]}, TxHash: tx.Hash(), BlockNumber: f.header.Number, BlockHash: f.header.Hash()}
	f.consumed = true
	if f.loseAfterSend && len(f.sent) == 1 {
		return common.Hash{}, errors.New("response lost after inclusion")
	}
	return tx.Hash(), nil
}

func fixture(t *testing.T) (*Manager, *fakeEth, *job, *[]string) {
	t.Helper()
	cfg := signing.Config{ChainID: 31337, Settlement: "0x0000000000000000000000000000000000003333", CashToken: "0x0000000000000000000000000000000000001111", SecurityToken: "0x0000000000000000000000000000000000002222", MM1: "0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC", MM2: "0x90F79bf6EB2c4f870365E785982E1f101E93b906", MM3: "0x15d34AAf54267DB7D7c367839AAf71A00a2C6A65"}
	fake := &fakeEth{config: cfg, chain: hexutil.Big(*big.NewInt(31337)), header: &types.Header{Number: big.NewInt(1), Time: 101, Difficulty: big.NewInt(0), GasLimit: 30000000, BaseFee: big.NewInt(1)}, logs: []types.Log{}}
	server := rpc.NewServer()
	if err := server.RegisterName("eth", fake); err != nil {
		t.Fatal(err)
	}
	rpcServer := httptest.NewServer(server)
	t.Cleanup(rpcServer.Close)
	notifications := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			TradeID string `json:"trade_id"`
			Status  string `json:"status"`
		}
		_ = json.NewDecoder(r.Body).Decode(&input)
		notifications = append(notifications, input.Status)
		if input.Status == "SETTLED" && len(notifications) > 1 && notifications[len(notifications)-2] != "SETTLED" {
			w.WriteHeader(503)
			return
		}
		_ = json.NewEncoder(w).Encode(engine.Trade{TradeID: input.TradeID, Status: input.Status})
	}))
	t.Cleanup(upstream.Close)
	m, err := New(context.Background(), rpcServer.URL, cfg, engine.NewClient(upstream.URL))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	key, _ := crypto.HexToECDSA(strings.Repeat("0", 59) + "a11ce")
	rfq := engine.Rfq{RfqID: "rfq-1", TraderID: crypto.PubkeyToAddress(key.PublicKey).Hex(), QuoteExpiresAt: 120, SettlementDeadline: 200}
	q := engine.Quote{TradeID: "0x" + strings.Repeat("1", 64), MakerID: "MM2", Side: "BUY", Cash: "98.000000", Quantity: "1.000000"}
	trade := engine.Trade{TradeID: q.TradeID, RfqID: rfq.RfqID, TraderID: rfq.TraderID, Quote: q, QuoteExpiresAt: rfq.QuoteExpiresAt, SettlementDeadline: rfq.SettlementDeadline, Status: "RESERVED"}
	data, err := signing.Typed(cfg, rfq, q)
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := signing.DemoKeys(cfg)
	buyer, _ := signing.Sign(data, key)
	seller, _ := signing.Sign(data, keys["MM2"])
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.Enqueue(trade, data, buyer, seller); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(m.jobs) != 1 {
		t.Fatal("double enqueue")
	}
	return m, fake, m.jobs[q.TradeID], &notifications
}
func TestLostRPCResponseAndRustNotificationKeepSameTransaction(t *testing.T) {
	m, f, j, notifications := fixture(t)
	f.loseSend = true
	ctx := context.Background()
	if m.step(ctx, j) == nil {
		t.Fatal("expected RPC error")
	}
	if _, ok := m.Get(j.trade.TradeID); !ok {
		t.Fatal("job lost")
	}
	if err := m.step(ctx, j); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 2 || f.sent[0].Hash() != f.sent[1].Hash() {
		t.Fatal("transaction changed on retry")
	}
	if m.step(ctx, j) == nil {
		t.Fatal("expected lost Rust response")
	}
	if result, _ := m.Get(j.trade.TradeID); result.Status != "SETTLEMENT_PENDING" {
		t.Fatal("settled before Rust acknowledgement")
	}
	if err := m.step(ctx, j); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 2 {
		t.Fatal("extra transaction")
	}
	if result, _ := m.Get(j.trade.TradeID); result.Status != "SETTLED" {
		t.Fatal(result)
	}
	if (*notifications)[len(*notifications)-1] != "SETTLED" {
		t.Fatal(*notifications)
	}
}
func TestRevertRetainsReservationUntilChainDeadline(t *testing.T) {
	m, f, j, notifications := fixture(t)
	ctx := context.Background()
	if err := m.step(ctx, j); err != nil {
		t.Fatal(err)
	}
	f.consumed = false
	f.receipt.Status = 0
	f.logs = nil
	if m.step(ctx, j) == nil {
		t.Fatal("expected revert")
	}
	for _, status := range *notifications {
		if status == "SETTLEMENT_FAILED" {
			t.Fatal("early release")
		}
	}
	f.header.Time = j.trade.SettlementDeadline
	if err := m.step(ctx, j); err != nil {
		t.Fatal(err)
	}
	if result, _ := m.Get(j.trade.TradeID); result.Status != "SETTLEMENT_FAILED" {
		t.Fatal(result)
	}
}
func TestWrongEventAmountsCannotSettleInventory(t *testing.T) {
	m, f, j, _ := fixture(t)
	f.wrongAmounts = true
	if err := m.step(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if m.step(context.Background(), j) == nil {
		t.Fatal("wrong amount accepted")
	}
	if result, _ := m.Get(j.trade.TradeID); result.Status != "SETTLEMENT_PENDING" {
		t.Fatal(result)
	}
}

func TestLostResponseAfterInclusionDoesNotResubmit(t *testing.T) {
	m, f, j, _ := fixture(t)
	f.loseAfterSend = true
	if m.step(context.Background(), j) == nil {
		t.Fatal("expected response loss")
	}
	_ = m.step(context.Background(), j) // The first Rust notification response is lost.
	if err := m.step(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 1 {
		t.Fatal("resubmitted after inclusion")
	}
	if result, _ := m.Get(j.trade.TradeID); result.Status != "SETTLED" {
		t.Fatal(result)
	}
}
func TestExpiredSignedNonceIsNotAbandoned(t *testing.T) {
	m, f, j, _ := fixture(t)
	f.loseSend = true
	if m.step(context.Background(), j) == nil {
		t.Fatal("expected RPC error")
	}
	f.header.Time = j.trade.SettlementDeadline
	if m.step(context.Background(), j) == nil {
		t.Fatal("must wait for nonce receipt")
	}
	if len(f.sent) != 2 || f.sent[0].Hash() != f.sent[1].Hash() {
		t.Fatal("expired nonce abandoned or changed")
	}
	if err := m.step(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if result, _ := m.Get(j.trade.TradeID); result.Status != "SETTLEMENT_FAILED" {
		t.Fatal(result)
	}
}
