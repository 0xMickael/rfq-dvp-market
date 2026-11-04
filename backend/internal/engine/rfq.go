package engine

import "encoding/json"

type Quote struct {
	QuoteID   string `json:"quote_id"`
	TradeID   string `json:"trade_id"`
	MakerID   string `json:"maker_id"`
	Side      string `json:"side"`
	UnitPrice string `json:"unit_price"`
	Quantity  string `json:"quantity_tbill"`
	Cash      string `json:"cash_amount"`
}
type Trade struct {
	TransactionHash    string `json:"transaction_hash"`
	TradeID            string `json:"trade_id"`
	RfqID              string `json:"rfq_id"`
	TraderID           string `json:"trader_id"`
	Status             string `json:"status"`
	Quote              Quote  `json:"quote"`
	AcceptedAt         uint64 `json:"accepted_at"`
	QuoteExpiresAt     uint64 `json:"quote_expires_at"`
	SettlementDeadline uint64 `json:"settlement_deadline"`
	ReservedAsset      string `json:"reserved_asset"`
	ReservedAmount     string `json:"reserved_amount"`
}
type Rfq struct {
	RfqID              string          `json:"rfq_id"`
	TraderID           string          `json:"trader_id"`
	Status             string          `json:"status"`
	CreatedAt          uint64          `json:"created_at"`
	QuoteExpiresAt     uint64          `json:"quote_expires_at"`
	SettlementDeadline uint64          `json:"settlement_deadline"`
	Quotes             []Quote         `json:"quotes"`
	Rejections         json.RawMessage `json:"rejections"`
	Trade              *Trade          `json:"trade"`
}
