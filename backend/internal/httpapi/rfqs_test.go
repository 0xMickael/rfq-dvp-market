package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"rfq-dvp-market.local/backend/internal/engine"
	"rfq-dvp-market.local/backend/internal/signing"
)

func demoConfig() signing.Config {
	return signing.Config{ChainID: 31337, Settlement: "0x0000000000000000000000000000000000003333", CashToken: "0x0000000000000000000000000000000000001111", SecurityToken: "0x0000000000000000000000000000000000002222", MM1: "0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC", MM2: "0x90F79bf6EB2c4f870365E785982E1f101E93b906", MM3: "0x15d34AAf54267DB7D7c367839AAf71A00a2C6A65"}
}

func TestVerifiedSignaturePrecedesReservationAndMMSigning(t *testing.T) {
	for _, side := range []string{"BUY", "SELL"} {
		for _, mode := range []string{"success", "invalid-signature", "altered-signature", "expired", "insufficient", "mismatched-reservation", "lost-response", "deadline-after-reserve"} {
			t.Run(side+"/"+mode, func(t *testing.T) {
				key, _ := crypto.HexToECDSA(strings.Repeat("0", 59) + "a11ce")
				trader := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())
				q := engine.Quote{QuoteID: "quote-2", TradeID: "0x" + strings.Repeat("1", 64), MakerID: "MM2", Side: side, UnitPrice: "98.000000", Quantity: "100.000000", Cash: "9800.000000"}
				rfq := engine.Rfq{RfqID: "rfq-1", TraderID: trader, Status: "QUOTED", CreatedAt: 1000, QuoteExpiresAt: 1030, SettlementDeadline: 1300, Quotes: []engine.Quote{q}, Rejections: json.RawMessage(`[]`)}
				if mode == "expired" {
					rfq.Status = "EXPIRED"
				}
				now := int64(1001)
				posts := 0
				var mu sync.Mutex
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					if r.Method == "GET" {
						writeJSON(w, 200, rfq)
						return
					}
					posts++
					var input map[string]string
					_ = json.NewDecoder(r.Body).Decode(&input)
					if input["trade_id"] != q.TradeID || input["trader_id"] != trader {
						t.Error("réservation non liée à la signature")
					}
					if mode == "insufficient" {
						fail(w, 409, "INSUFFICIENT_INVENTORY", "insuffisant")
						return
					}
					asset, amount := "tBILL", q.Quantity
					if side == "SELL" {
						asset, amount = "sEUR", q.Cash
					}
					reserved := engine.Trade{TradeID: q.TradeID, RfqID: rfq.RfqID, TraderID: trader, Status: "RESERVED", Quote: q, AcceptedAt: 1001, QuoteExpiresAt: 1030, SettlementDeadline: 1300, ReservedAsset: asset, ReservedAmount: amount}
					rfq.Status = "ACCEPTED"
					rfq.Trade = &reserved
					if mode == "mismatched-reservation" {
						reserved.Quote.Cash = "1.000000"
					}
					if mode == "lost-response" && posts == 1 {
						w.WriteHeader(500)
						_, _ = w.Write([]byte(`{}`))
						return
					}
					if mode == "deadline-after-reserve" {
						now = 1300
					}
					writeJSON(w, 200, reserved)
				}))
				defer upstream.Close()
				service, err := NewSigningService(engine.NewClient(upstream.URL), demoConfig())
				if err != nil {
					t.Fatal(err)
				}
				service.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return time.Unix(now, 0) }
				data, err := signing.Typed(demoConfig(), rfq, q)
				if err != nil {
					t.Fatal(err)
				}
				toSign := data
				if mode == "altered-signature" {
					altered := q
					altered.Cash = "1.000000"
					toSign, _ = signing.Typed(demoConfig(), rfq, altered)
				}
				signature, _ := signing.Sign(toSign, key)
				if mode == "invalid-signature" {
					signature = "0x00"
				}
				payload, _ := json.Marshal(map[string]string{"quote_id": q.QuoteID, "trade_id": q.TradeID, "signature": signature})
				handler := Handler(service.client, service)
				call := func() *httptest.ResponseRecorder {
					req := httptest.NewRequest("POST", "/api/rfqs/rfq-1/accept", strings.NewReader(string(payload)))
					req.Header.Set("Content-Type", "application/json")
					result := httptest.NewRecorder()
					handler.ServeHTTP(result, req)
					return result
				}
				result := call()
				if mode == "lost-response" {
					if result.Code != 503 || strings.Contains(result.Body.String(), "buyer_signature") {
						t.Fatal(result.Body.String())
					}
					result = call()
				}
				if mode == "success" || mode == "lost-response" {
					if result.Code != 200 {
						t.Fatalf("%d %s", result.Code, result.Body.String())
					}
					var signed SignedTrade
					if err := json.Unmarshal(result.Body.Bytes(), &signed); err != nil {
						t.Fatal(err)
					}
					if err := signing.Verify(data, signed.BuyerSignature, data.Message["buyer"].(string)); err != nil {
						t.Fatal(err)
					}
					if err := signing.Verify(data, signed.SellerSignature, data.Message["seller"].(string)); err != nil {
						t.Fatal(err)
					}
					repeat := call()
					if repeat.Code != 200 || repeat.Body.String() != result.Body.String() {
						t.Fatal("retry non identique")
					}
					if posts != 1 {
						t.Fatal("double réservation", posts)
					}
				} else {
					if result.Code < 400 || strings.Contains(result.Body.String(), "buyer_signature") {
						t.Fatal("signature MM émise malgré échec", result.Body.String())
					}
					if (mode == "invalid-signature" || mode == "altered-signature" || mode == "expired") && posts != 0 {
						t.Fatal("réservation avant validation")
					}
				}
			})
		}
	}
}

func TestSigningConfigurationAndDisabledMode(t *testing.T) {
	cfg := demoConfig()
	cfg.ChainID = 1
	if _, err := NewSigningService(nil, cfg); err == nil {
		t.Fatal("mainnet accepté")
	}
	cfg = demoConfig()
	cfg.MM1 = cfg.MM2
	if _, err := NewSigningService(nil, cfg); err == nil {
		t.Fatal("mauvaise clé MM acceptée")
	}
	handler := Handler(engine.NewClient("http://127.0.0.1:1"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/config", nil))
	if !strings.Contains(response.Body.String(), `"signing_enabled":false`) {
		t.Fatal(response.Body.String())
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/api/rfqs", strings.NewReader(`{}`)))
	if response.Code != 503 {
		t.Fatal(response.Code)
	}
}
