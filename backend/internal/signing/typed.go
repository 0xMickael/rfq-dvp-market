package signing

import (
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	ethmath "github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"rfq-dvp-market.local/backend/internal/engine"
)

type Config struct {
	ChainID       int64  `json:"chainId"`
	Settlement    string `json:"settlement"`
	CashToken     string `json:"cashToken"`
	SecurityToken string `json:"securityToken"`
	MM1           string `json:"mm1"`
	MM2           string `json:"mm2"`
	MM3           string `json:"mm3"`
}

func Address(s string) (string, error) {
	if !common.IsHexAddress(s) || common.HexToAddress(s) == (common.Address{}) {
		return "", fmt.Errorf("adresse EVM non nulle requise")
	}
	return strings.ToLower(common.HexToAddress(s).Hex()), nil
}

func (c Config) Maker(id string) string {
	switch id {
	case "MM1":
		return c.MM1
	case "MM2":
		return c.MM2
	case "MM3":
		return c.MM3
	}
	return ""
}

func (c Config) Validate() error {
	if c.ChainID != 31337 {
		return fmt.Errorf("signer de démonstration réservé à Anvil 31337")
	}
	seen := map[string]bool{}
	for _, s := range []string{c.Settlement, c.CashToken, c.SecurityToken, c.MM1, c.MM2, c.MM3} {
		a, err := Address(s)
		if err != nil || seen[a] {
			return fmt.Errorf("adresses de déploiement invalides ou dupliquées")
		}
		seen[a] = true
	}
	return nil
}

var decimal = regexp.MustCompile(`^[0-9]+\.[0-9]{6}$`)
var tradeID = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)

func atoms(s string) (string, error) {
	if !decimal.MatchString(s) {
		return "", fmt.Errorf("montant Rust invalide")
	}
	n, ok := new(big.Int).SetString(strings.ReplaceAll(s, ".", ""), 10)
	if !ok || n.Sign() <= 0 || !n.IsUint64() {
		return "", fmt.Errorf("montant hors limites")
	}
	return n.String(), nil
}

func Typed(c Config, r engine.Rfq, q engine.Quote) (apitypes.TypedData, error) {
	var empty apitypes.TypedData
	trader, err := Address(r.TraderID)
	if err != nil {
		return empty, err
	}
	maker, err := Address(c.Maker(q.MakerID))
	if err != nil || trader == maker {
		return empty, fmt.Errorf("contreparties invalides")
	}
	if !tradeID.MatchString(q.TradeID) || common.HexToHash(q.TradeID) == (common.Hash{}) || r.SettlementDeadline <= r.QuoteExpiresAt {
		return empty, fmt.Errorf("identifiant ou échéances invalides")
	}
	cash, err := atoms(q.Cash)
	if err != nil {
		return empty, err
	}
	security, err := atoms(q.Quantity)
	if err != nil {
		return empty, err
	}
	buyer, seller := trader, maker
	switch q.Side {
	case "BUY":
	case "SELL":
		buyer, seller = maker, trader
	default:
		return empty, fmt.Errorf("side invalide")
	}
	return apitypes.TypedData{
		PrimaryType: "Trade",
		Domain:      apitypes.TypedDataDomain{Name: "rfq-dvp-market", Version: "1", ChainId: ethmath.NewHexOrDecimal256(c.ChainID), VerifyingContract: c.Settlement},
		Types: apitypes.Types{
			"EIP712Domain": {{Name: "name", Type: "string"}, {Name: "version", Type: "string"}, {Name: "chainId", Type: "uint256"}, {Name: "verifyingContract", Type: "address"}},
			"Trade":        {{Name: "tradeId", Type: "bytes32"}, {Name: "buyer", Type: "address"}, {Name: "seller", Type: "address"}, {Name: "cashToken", Type: "address"}, {Name: "securityToken", Type: "address"}, {Name: "cashAmount", Type: "uint256"}, {Name: "securityAmount", Type: "uint256"}, {Name: "deadline", Type: "uint256"}},
		},
		Message: apitypes.TypedDataMessage{"tradeId": q.TradeID, "buyer": buyer, "seller": seller, "cashToken": c.CashToken, "securityToken": c.SecurityToken, "cashAmount": cash, "securityAmount": security, "deadline": strconv.FormatUint(r.SettlementDeadline, 10)},
	}, nil
}

func Digest(data apitypes.TypedData) ([]byte, error) {
	hash, _, err := apitypes.TypedDataAndHash(data)
	return hash, err
}

// Ne pas sérialiser le salt vide de go-ethereum : ce champ ne fait pas partie du domaine.
func WalletData(data apitypes.TypedData) map[string]any {
	return map[string]any{"domain": data.Domain.Map(), "types": data.Types, "primaryType": data.PrimaryType, "message": data.Message}
}

func Verify(data apitypes.TypedData, signature, expected string) error {
	sig, err := hex.DecodeString(strings.TrimPrefix(signature, "0x"))
	if err != nil || len(sig) != 65 {
		return fmt.Errorf("signature de 65 octets requise")
	}
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	if !crypto.ValidateSignatureValues(sig[64], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:64]), true) {
		return fmt.Errorf("signature non canonique")
	}
	hash, err := Digest(data)
	if err != nil {
		return err
	}
	pub, err := crypto.SigToPub(hash, sig)
	if err != nil {
		return err
	}
	if !strings.EqualFold(crypto.PubkeyToAddress(*pub).Hex(), expected) {
		return fmt.Errorf("signature trader invalide pour ces termes")
	}
	return nil
}

func Sign(data apitypes.TypedData, key *ecdsa.PrivateKey) (string, error) {
	hash, err := Digest(data)
	if err != nil {
		return "", err
	}
	sig, err := crypto.Sign(hash, key)
	if err != nil {
		return "", err
	}
	sig[64] += 27
	return "0x" + hex.EncodeToString(sig), nil
}

// Appeler après Verify : OpenZeppelin attend v = 27 ou 28.
func CanonicalSignature(signature string) string {
	sig, _ := hex.DecodeString(strings.TrimPrefix(signature, "0x"))
	if sig[64] < 27 {
		sig[64] += 27
	}
	return "0x" + hex.EncodeToString(sig)
}

// Clés publiques de test Anvil, indices 2, 3 et 4 du mnemonic standard. Jamais en production.
func DemoKeys(c Config) (map[string]*ecdsa.PrivateKey, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	raw := map[string]string{
		"MM1": "5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a",
		"MM2": "7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6",
		"MM3": "47e179ec197488593b187f80a00eb0da91f1b9d0b13f8733639f19c30a34926a",
	}
	keys := make(map[string]*ecdsa.PrivateKey)
	for id, s := range raw {
		key, err := crypto.HexToECDSA(s)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(crypto.PubkeyToAddress(key.PublicKey).Hex(), c.Maker(id)) {
			return nil, fmt.Errorf("adresse %s incompatible avec la clé Anvil", id)
		}
		keys[id] = key
	}
	return keys, nil
}
