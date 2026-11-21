package main

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"rfq-dvp-market.local/backend/internal/engine"
)

type makerView struct {
	ID             string `json:"id"`
	Tbill          string `json:"tbill"`
	Cash           string `json:"cash"`
	ReservedTbill  string `json:"reserved_tbill"`
	ReservedCash   string `json:"reserved_cash"`
	AvailableTbill string `json:"available_tbill"`
	AvailableCash  string `json:"available_cash"`
}
type marketView struct {
	Makers []makerView `json:"makers"`
}
type balances struct{ cash, tbill *big.Int }
type baseline struct {
	market                  marketView
	holdings                map[string]balances
	cashSupply, tbillSupply *big.Int
	block                   *big.Int
}

func (c *client) baseline(ctx context.Context) (baseline, error) {
	var b baseline
	if err := c.call(ctx, "/api/market", nil, &b.market); err != nil {
		return b, err
	}
	if len(b.market.Makers) != 3 {
		return b, fmt.Errorf("three market makers required")
	}
	for _, maker := range b.market.Makers {
		if maker.ReservedCash != "0.000000" || maker.ReservedTbill != "0.000000" {
			return b, fmt.Errorf("isolated campaign required: initial reservations are nonzero")
		}
	}
	header, err := c.rpc.HeaderByNumber(ctx, nil)
	if err != nil {
		return b, err
	}
	b.block = new(big.Int).Set(header.Number)
	b.holdings = map[string]balances{}
	addresses := []string{c.config.MM1, c.config.MM2, c.config.MM3}
	for _, w := range c.wallets {
		addresses = append(addresses, w.address)
	}
	for _, address := range addresses {
		cash, err := c.tokenValue(ctx, c.config.CashToken, "balanceOf", b.block, common.HexToAddress(address))
		if err != nil {
			return b, err
		}
		tbill, err := c.tokenValue(ctx, c.config.SecurityToken, "balanceOf", b.block, common.HexToAddress(address))
		if err != nil {
			return b, err
		}
		b.holdings[strings.ToLower(address)] = balances{cash, tbill}
	}
	b.cashSupply, err = c.tokenValue(ctx, c.config.CashToken, "totalSupply", b.block)
	if err != nil {
		return b, err
	}
	b.tbillSupply, err = c.tokenValue(ctx, c.config.SecurityToken, "totalSupply", b.block)
	return b, err
}
func (run *campaign) verify(ctx context.Context, b baseline) invariantReport {
	result := invariantReport{Errors: append([]string{}, run.failures...)}
	fail := func(message string) { result.Errors = append(result.Errors, message) }
	c := run.client
	expectedBooked := map[string]balances{}
	expectedReserved := map[string]balances{}
	for _, maker := range b.market.Makers {
		cash, e1 := atoms(maker.Cash)
		tbill, e2 := atoms(maker.Tbill)
		if e1 != nil || e2 != nil {
			fail("invalid initial snapshot")
			return result
		}
		expectedBooked[maker.ID] = balances{cash, tbill}
		expectedReserved[maker.ID] = balances{new(big.Int), new(big.Int)}
	}
	seenTrades := map[string]bool{}
	settled := map[string]engine.Trade{}
	for _, record := range run.records {
		var rfq engine.Rfq
		if err := c.call(ctx, "/api/rfqs/"+record.rfq.RfqID, nil, &rfq); err != nil {
			fail(err.Error())
			continue
		}
		result.CheckedRFQs++
		if rfq.TraderID != record.rfq.TraderID {
			fail("RFQ identity changed")
		}
		if rfq.Trade == nil {
			if record.acknowledged {
				fail("acknowledged acceptance without a durable trade: " + rfq.RfqID)
			}
			continue
		}
		trade := *rfq.Trade
		if c.options.Mode == "settlement" && (trade.Status == "RESERVED" || trade.Status == "SETTLEMENT_PENDING") {
			_, err := run.waitSettlement(ctx, trade.TradeID, false)
			if err != nil {
				fail("unresolved job: " + trade.TradeID)
				continue
			}
			if err = c.call(ctx, "/api/rfqs/"+rfq.RfqID, nil, &rfq); err != nil || rfq.Trade == nil {
				fail("trade missing after drain")
				continue
			}
			trade = *rfq.Trade
		}
		result.Trades++
		if seenTrades[trade.TradeID] {
			fail("duplicate trade")
		}
		seenTrades[trade.TradeID] = true
		if trade.Quote != record.quote || trade.TraderID != record.rfq.TraderID || trade.TradeID != record.quote.TradeID || trade.AcceptedAt >= trade.QuoteExpiresAt {
			fail("inconsistent trade terms")
		}
		cash, err := atoms(trade.Quote.Cash)
		if err != nil {
			fail("invalid cash amount")
			continue
		}
		tbill, err := atoms(trade.Quote.Quantity)
		if err != nil {
			fail("invalid quantity")
			continue
		}
		booked, ok := expectedBooked[trade.Quote.MakerID]
		if !ok {
			fail("unknown market maker")
			continue
		}
		reserved := expectedReserved[trade.Quote.MakerID]
		switch trade.Status {
		case "RESERVED", "SETTLEMENT_PENDING":
			result.Pending++
			if trade.Quote.Side == "BUY" {
				reserved.tbill.Add(reserved.tbill, tbill)
			} else {
				reserved.cash.Add(reserved.cash, cash)
			}
		case "SETTLEMENT_FAILED":
			result.Failed++
		case "SETTLED":
			result.Settled++
			settled[trade.TradeID] = trade
			direction := int64(1)
			if trade.Quote.Side == "SELL" {
				direction = -1
			}
			cashDelta := new(big.Int).Mul(cash, big.NewInt(direction))
			tbillDelta := new(big.Int).Mul(tbill, big.NewInt(direction))
			booked.cash.Add(booked.cash, cashDelta)
			booked.tbill.Sub(booked.tbill, tbillDelta)
			maker := b.holdings[strings.ToLower(c.config.Maker(trade.Quote.MakerID))]
			trader, ok := b.holdings[strings.ToLower(trade.TraderID)]
			if !ok {
				fail("unknown trader")
				continue
			}
			maker.cash.Add(maker.cash, cashDelta)
			maker.tbill.Sub(maker.tbill, tbillDelta)
			trader.cash.Sub(trader.cash, cashDelta)
			trader.tbill.Add(trader.tbill, tbillDelta)
		default:
			fail("unknown trade status")
		}
	}
	if result.Trades < run.report.Accepted {
		fail("fewer trades than acknowledged acceptances")
	}
	if c.options.Mode == "quotes" && result.Trades != 0 {
		fail("trade created in a quotes campaign")
	}
	if c.options.Mode == "accept" && (result.Settled != 0 || result.Failed != 0) {
		fail("worker active during the reservation campaign")
	}
	if c.options.Mode == "settlement" && (result.Pending != 0 || result.Failed != 0) {
		fail("pending or failed settlements at the end of the campaign")
	}
	if len(result.Errors) > 0 {
		return result
	}
	var final marketView
	if err := c.call(ctx, "/api/market", nil, &final); err != nil {
		fail(err.Error())
		return result
	}
	if len(final.Makers) != len(expectedBooked) {
		fail("inconsistent market maker count")
	}
	for _, maker := range final.Makers {
		booked, ok := expectedBooked[maker.ID]
		if !ok {
			fail("unknown final market maker")
			continue
		}
		reserved := expectedReserved[maker.ID]
		for _, asset := range []struct {
			total, reserved, available      string
			expectedTotal, expectedReserved *big.Int
		}{{maker.Cash, maker.ReservedCash, maker.AvailableCash, booked.cash, reserved.cash}, {maker.Tbill, maker.ReservedTbill, maker.AvailableTbill, booked.tbill, reserved.tbill}} {
			total, e1 := atoms(asset.total)
			reserved, e2 := atoms(asset.reserved)
			available, e3 := atoms(asset.available)
			if e1 != nil || e2 != nil || e3 != nil {
				fail("invalid final amounts")
				continue
			}
			if available.Sign() < 0 || total.Cmp(new(big.Int).Add(reserved, available)) != 0 || total.Cmp(asset.expectedTotal) != 0 || reserved.Cmp(asset.expectedReserved) != 0 {
				fail("inconsistent inventory or reservations: " + maker.ID)
			}
		}
	}
	header, err := c.rpc.HeaderByNumber(ctx, nil)
	if err != nil {
		fail("final RPC read unavailable")
		return result
	}
	for address, expected := range b.holdings {
		for _, asset := range []struct {
			token  string
			amount *big.Int
		}{{c.config.CashToken, expected.cash}, {c.config.SecurityToken, expected.tbill}} {
			actual, err := c.tokenValue(ctx, asset.token, "balanceOf", header.Number, common.HexToAddress(address))
			if err != nil || actual.Cmp(asset.amount) != 0 {
				fail("inconsistent on-chain balance: " + address)
			}
		}
	}
	for _, asset := range []struct {
		token  string
		supply *big.Int
	}{{c.config.CashToken, b.cashSupply}, {c.config.SecurityToken, b.tbillSupply}} {
		actual, err := c.tokenValue(ctx, asset.token, "totalSupply", header.Number)
		if err != nil || actual.Cmp(asset.supply) != 0 {
			fail("token supply changed during the campaign")
		}
	}
	eventABI := mustABI(`[{"type":"event","name":"TradeSettled","inputs":[{"name":"tradeId","type":"bytes32","indexed":true},{"name":"buyer","type":"address","indexed":true},{"name":"seller","type":"address","indexed":true},{"name":"cashAmount","type":"uint256","indexed":false},{"name":"securityAmount","type":"uint256","indexed":false}]}]`)
	event := eventABI.Events["TradeSettled"]
	var logs []types.Log
	if header.Number.Cmp(b.block) > 0 {
		logs, err = c.rpc.FilterLogs(ctx, ethereum.FilterQuery{FromBlock: new(big.Int).Add(b.block, big.NewInt(1)), ToBlock: header.Number, Addresses: []common.Address{common.HexToAddress(c.config.Settlement)}, Topics: [][]common.Hash{{event.ID}}})
	}
	if err != nil {
		fail("cannot read settlement events")
	} else {
		counts := map[string]int{}
		for _, log := range logs {
			if len(log.Topics) != 4 {
				fail("invalid event")
				continue
			}
			id := log.Topics[1].Hex()
			trade, ok := settled[id]
			if !ok {
				fail("event has no matching settled trade")
				continue
			}
			counts[id]++
			values, err := event.Inputs.NonIndexed().Unpack(log.Data)
			cash, _ := atoms(trade.Quote.Cash)
			tbill, _ := atoms(trade.Quote.Quantity)
			buyer, seller := trade.TraderID, c.config.Maker(trade.Quote.MakerID)
			if trade.Quote.Side == "SELL" {
				buyer, seller = seller, buyer
			}
			if err != nil || values[0].(*big.Int).Cmp(cash) != 0 || values[1].(*big.Int).Cmp(tbill) != 0 || log.Topics[2] != common.BytesToHash(common.HexToAddress(buyer).Bytes()) || log.Topics[3] != common.BytesToHash(common.HexToAddress(seller).Bytes()) || log.TxHash.Hex() != trade.TransactionHash || log.Removed {
				fail("event differs from accepted terms")
			}
		}
		for id := range settled {
			if counts[id] != 1 {
				fail("incorrect event count: " + id)
			}
		}
	}
	current, err := c.rpc.HeaderByNumber(ctx, header.Number)
	if err != nil || current.Hash() != header.Hash() {
		fail("block changed during audit")
	}
	result.Passed = len(result.Errors) == 0
	return result
}
