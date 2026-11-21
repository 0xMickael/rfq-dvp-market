package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"rfq-dvp-market.local/backend/internal/signing"
)

type wallet struct {
	key     *ecdsa.PrivateKey
	address string
	mu      sync.Mutex
}
type client struct {
	options options
	http    *http.Client
	rpc     *ethclient.Client
	config  signing.Config
	wallets []*wallet
}
type apiFailure struct {
	Status  int
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *apiFailure) Error() string { return fmt.Sprintf("HTTP %d %s", e.Status, e.Code) }
func localURL(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("HTTP 127.0.0.1 required for local campaigns")
	}
	return nil
}
func newClient(ctx context.Context, o options) (*client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxIdleConnsPerHost = o.Concurrency * 2
	transport.MaxIdleConns = o.Concurrency * 2
	c := &client{options: o, http: &http.Client{Timeout: o.Timeout, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("redirects are not allowed") }}}
	var settings struct {
		Deployment signing.Config `json:"deployment"`
		Settlement bool           `json:"settlement_enabled"`
	}
	if err := c.call(ctx, "/api/config", nil, &settings); err != nil {
		return nil, err
	}
	c.config = settings.Deployment
	if err := c.config.Validate(); err != nil {
		return nil, err
	}
	if !settings.Settlement {
		return nil, fmt.Errorf("backend with RPC and settlement required")
	}
	rpcClient, err := rpc.DialOptions(ctx, o.RPC, rpc.WithHTTPClient(c.http))
	if err != nil {
		return nil, err
	}
	c.rpc = ethclient.NewClient(rpcClient)
	probe, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	id, err := c.rpc.ChainID(probe)
	if err != nil || id.Int64() != 31337 {
		c.close()
		return nil, fmt.Errorf("Anvil chain 31337 required")
	}
	var version string
	if err = rpcClient.CallContext(probe, &version, "web3_clientVersion"); err != nil || !strings.Contains(strings.ToLower(version), "anvil") {
		c.close()
		return nil, fmt.Errorf("Anvil node required")
	}
	for i := 0; i < o.Traders; i++ {
		key, err := crypto.ToECDSA(crypto.Keccak256([]byte(fmt.Sprintf("rfq-dvp-market-public-loadgen:%d:%d", o.Seed, i))))
		if err != nil {
			c.close()
			return nil, err
		}
		address := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())
		c.wallets = append(c.wallets, &wallet{key: key, address: address})
	}
	return c, nil
}
func (c *client) close() {
	if c.rpc != nil {
		c.rpc.Close()
	}
	c.http.CloseIdleConnections()
}
func (c *client) call(ctx context.Context, path string, input, out any) error {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	method := "GET"
	if input != nil {
		method = "POST"
	}
	req, err := http.NewRequestWithContext(ctx, method, c.options.API+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP %s : %w", path, err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	if err != nil {
		return err
	}
	if len(raw) > 1024*1024 {
		return fmt.Errorf("response too large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		failure := &apiFailure{Status: response.StatusCode}
		_ = json.Unmarshal(raw, failure)
		return failure
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}
func atoms(value string) (*big.Int, error) {
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return nil, fmt.Errorf("invalid amount")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if fraction == "" || len(fraction) > 6 {
			return nil, fmt.Errorf("invalid decimal places")
		}
	}
	for _, ch := range value {
		if (ch < '0' || ch > '9') && ch != '.' {
			return nil, fmt.Errorf("invalid amount")
		}
	}
	result, ok := new(big.Int).SetString(parts[0]+fraction+strings.Repeat("0", 6-len(fraction)), 10)
	if !ok {
		return nil, fmt.Errorf("invalid amount")
	}
	return result, nil
}
func decimal(value int64) string { return fmt.Sprintf("%d.%06d", value/1000000, value%1000000) }
