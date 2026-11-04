package signing

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	ethmath "github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"rfq-dvp-market.local/backend/internal/engine"
)

func fixture(t *testing.T) (apitypes.TypedData, map[string]string) {
	t.Helper()
	raw, err := os.ReadFile("../../../contracts/fixtures/buy-100.typed-data.json")
	if err != nil {
		t.Fatal(err)
	}
	var data apitypes.TypedData
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile("../../../contracts/fixtures/buy-100.signatures.json")
	if err != nil {
		t.Fatal(err)
	}
	var signatures map[string]string
	if err := json.Unmarshal(raw, &signatures); err != nil {
		t.Fatal(err)
	}
	return data, signatures
}
func TestSolidityCastFixtureAndAllTermsAreBound(t *testing.T) {
	data, sigs := fixture(t)
	for _, role := range []string{"buyer", "seller"} {
		if err := Verify(data, sigs[role+"Signature"], data.Message[role].(string)); err != nil {
			t.Fatal(err)
		}
	}
	for _, field := range []string{"tradeId", "buyer", "seller", "cashToken", "securityToken", "cashAmount", "securityAmount", "deadline"} {
		modified, _ := fixture(t)
		switch field {
		case "tradeId":
			modified.Message[field] = "0x" + strings.Repeat("1", 64)
		case "buyer", "seller", "cashToken", "securityToken":
			modified.Message[field] = "0x0000000000000000000000000000000000009999"
		default:
			modified.Message[field] = "1"
		}
		if Verify(modified, sigs["buyerSignature"], data.Message["buyer"].(string)) == nil {
			t.Fatalf("altération acceptée: %s", field)
		}
	}
	modified, _ := fixture(t)
	modified.Domain.VerifyingContract = "0x0000000000000000000000000000000000004444"
	if Verify(modified, sigs["buyerSignature"], data.Message["buyer"].(string)) == nil {
		t.Fatal("mauvais domaine accepté")
	}
	modified, _ = fixture(t)
	modified.Domain.ChainId = ethmath.NewHexOrDecimal256(1)
	if Verify(modified, sigs["buyerSignature"], data.Message["buyer"].(string)) == nil {
		t.Fatal("mauvaise chaîne acceptée")
	}
}

func TestConstructedTermsMatchFixtureAndSellDirection(t *testing.T) {
	data, sigs := fixture(t)
	config := Config{ChainID: 31337, Settlement: data.Domain.VerifyingContract, CashToken: data.Message["cashToken"].(string), SecurityToken: data.Message["securityToken"].(string), MM2: data.Message["seller"].(string)}
	r := engine.Rfq{TraderID: data.Message["buyer"].(string), QuoteExpiresAt: 1999999900, SettlementDeadline: 2000000000}
	q := engine.Quote{MakerID: "MM2", Side: "BUY", TradeID: data.Message["tradeId"].(string), Quantity: "100.000000", Cash: "9804.921500"}
	got, err := Typed(config, r, q)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(got, sigs["buyerSignature"], r.TraderID); err != nil {
		t.Fatal(err)
	}
	q.Side = "SELL"
	got, err = Typed(config, r, q)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(got.Message["seller"].(string), r.TraderID) || !strings.EqualFold(got.Message["buyer"].(string), config.MM2) {
		t.Fatal("orientation SELL incorrecte")
	}
	key, _ := crypto.HexToECDSA(strings.Repeat("0", 59) + "a11ce")
	sig, err := Sign(got, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(got, sig, r.TraderID); err != nil {
		t.Fatal(err)
	}
	q.Cash = "9007199254.740993"
	got, err = Typed(config, r, q)
	if err != nil || got.Message["cashAmount"] != "9007199254740993" {
		t.Fatal("précision perdue", err)
	}
	for _, bad := range []string{"-1.000000", "1.0000001", "1e3", "0.000000", "18446744073709.551616"} {
		if _, err := atoms(bad); err == nil {
			t.Fatal("montant invalide accepté", bad)
		}
	}
	for _, sig := range []string{"", "0x01", "0x" + strings.Repeat("00", 65)} {
		if Verify(data, sig, r.TraderID) == nil {
			t.Fatal("signature invalide acceptée")
		}
	}
}
