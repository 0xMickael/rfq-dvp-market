package main

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

var tokenABI = mustABI(`[{"type":"function","name":"mint","inputs":[{"type":"address"},{"type":"uint256"}],"outputs":[]},{"type":"function","name":"approve","inputs":[{"type":"address"},{"type":"uint256"}],"outputs":[{"type":"bool"}]},{"type":"function","name":"balanceOf","inputs":[{"type":"address"}],"outputs":[{"type":"uint256"}]},{"type":"function","name":"totalSupply","inputs":[],"outputs":[{"type":"uint256"}]}]`)

func mustABI(raw string) abi.ABI {
	value, err := abi.JSON(strings.NewReader(raw))
	if err != nil {
		panic(err)
	}
	return value
}

func (c *client) prepare(ctx context.Context) error {
	// Public Anvil issuer fixture, exclusively on the loopback Anvil checked at startup.
	issuer, err := crypto.HexToECDSA("ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80")
	if err != nil {
		return err
	}
	maximum := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	for _, trader := range c.wallets {
		address := common.HexToAddress(trader.address)
		if err = c.send(ctx, issuer, address, big.NewInt(1000000000000000000), nil); err != nil {
			return err
		}
		for _, asset := range []struct {
			token  string
			amount int64
		}{{c.config.CashToken, 100000000000}, {c.config.SecurityToken, 1000000000}} {
			token := common.HexToAddress(asset.token)
			data, err := tokenABI.Pack("mint", address, big.NewInt(asset.amount))
			if err != nil {
				return err
			}
			if err = c.send(ctx, issuer, token, big.NewInt(0), data); err != nil {
				return err
			}
			data, err = tokenABI.Pack("approve", common.HexToAddress(c.config.Settlement), maximum)
			if err != nil {
				return err
			}
			if err = c.send(ctx, trader.key, token, big.NewInt(0), data); err != nil {
				return err
			}
		}
	}
	return nil
}
func (c *client) send(ctx context.Context, key *ecdsa.PrivateKey, to common.Address, value *big.Int, data []byte) error {
	attempt, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	from := crypto.PubkeyToAddress(key.PublicKey)
	nonce, err := c.rpc.PendingNonceAt(attempt, from)
	if err != nil {
		return err
	}
	gas, err := c.rpc.EstimateGas(attempt, ethereum.CallMsg{From: from, To: &to, Value: value, Data: data})
	if err != nil {
		return err
	}
	price, err := c.rpc.SuggestGasPrice(attempt)
	if err != nil {
		return err
	}
	tx, err := types.SignTx(types.NewTx(&types.LegacyTx{Nonce: nonce, To: &to, Value: value, Gas: gas + gas/5, GasPrice: price, Data: data}), types.LatestSignerForChainID(big.NewInt(31337)), key)
	if err != nil {
		return err
	}
	if err = c.rpc.SendTransaction(attempt, tx); err != nil {
		return err
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		receipt, err := c.rpc.TransactionReceipt(attempt, tx.Hash())
		if err == nil {
			if receipt.Status != 1 {
				return fmt.Errorf("preparation transaction reverted")
			}
			return nil
		}
		if err != ethereum.NotFound {
			return err
		}
		select {
		case <-attempt.Done():
			return attempt.Err()
		case <-ticker.C:
		}
	}
}
func (c *client) tokenValue(ctx context.Context, token, method string, block *big.Int, args ...any) (*big.Int, error) {
	data, err := tokenABI.Pack(method, args...)
	if err != nil {
		return nil, err
	}
	to := common.HexToAddress(token)
	attempt, cancel := context.WithTimeout(ctx, c.options.Timeout)
	defer cancel()
	raw, err := c.rpc.CallContract(attempt, ethereum.CallMsg{To: &to, Data: data}, block)
	if err != nil {
		return nil, err
	}
	values, err := tokenABI.Unpack(method, raw)
	if err != nil {
		return nil, err
	}
	return values[0].(*big.Int), nil
}
