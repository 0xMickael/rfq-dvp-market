# API reference

The frontend proxies `/api` to Go on `127.0.0.1:8080`. Go calls the internal Rust service on `127.0.0.1:8081`. The endpoints are intended for a local market.

## Request a quote

Send `Content-Type: application/json`. Provide amounts as decimal strings with up to six fractional digits. A tBILL amount specifies an exact security quantity. An sEUR amount specifies a maximum BUY budget or minimum SELL proceeds.

```json
{"side":"BUY","unit":"tBILL","amount":"100"}
```

Send this to `POST /api/quotes` for indicative prices. For an executable RFQ, send it to `POST /api/rfqs` with `trader_id` set to the trader's wallet address. Creating an RFQ declares the wallet. Accepting a quote requires its signature. The RFQ returns `rfq_id`, `quote_expires_at`, `settlement_deadline`, quotes and any maker rejections. Quotes are ordered with the best price first. Each includes `quote_id`, `trade_id`, `maker_id`, `unit_price`, `quantity_tbill` and `cash_amount`.

## Follow the trade

| Endpoint | Purpose |
|---|---|
| `GET /api/config` | Signing and settlement availability, contract addresses and market-maker addresses |
| `GET /api/readiness` | 200 when the market admits new RFQs and acceptances, 503 while recovery is required |
| `GET /api/market` | Reference price, maker quotes and booked, reserved and available inventory |
| `POST /api/quotes` | Indicative quotes without a wallet |
| `POST /api/rfqs` | Create a wallet-bound RFQ |
| `GET /api/rfqs/{id}` | Read an RFQ and its trade, if accepted |
| `GET /api/rfqs/{id}/quotes/{quote}/terms` | Retrieve the prepared EIP-712 terms before signing |
| `POST /api/rfqs/{id}/accept` | Accept with `quote_id`, `trade_id` and the trader's `signature` |
| `GET /api/settlements/{trade}` | Poll the job status and transaction hash when available |
| `GET /api/balances/{account}` | Read the on-chain balances for an account |

Quote and settlement deadlines are Unix timestamps. Quotes can be accepted for 120 seconds from RFQ creation. A signed settlement can execute on-chain for 600 seconds from creation. Acceptance does not extend either deadline.

A successful initial acceptance returns the signed trade and, when RPC is enabled, a settlement job. Poll for `SETTLEMENT_PENDING`, `SETTLED` or `SETTLEMENT_FAILED`. The transaction hash can appear before broadcast is confirmed. Use the job status to track the outcome.

## Errors and retries

Business errors provide a machine-readable `code` and a human-readable `message`. Market responses may also include per-maker `rejections`. Common examples include `QUOTE_EXPIRED` (410), `NO_EXECUTABLE_QUOTE` (422) and `RECOVERY_REQUIRED` (503).

A timeout leaves the outcome uncertain. Retry acceptance with the same RFQ, quote, trade ID and signature. An existing settlement job can be retrieved this way without creating a second reservation. See [recovery](architecture.md#persistence-and-recovery).

## Signed terms

Both counterparties sign the same EIP-712 trade:

```text
Trade(bytes32 tradeId,address buyer,address seller,address cashToken,address securityToken,uint256 cashAmount,uint256 securityAmount,uint256 deadline)
```

The domain uses name `rfq-dvp-market`, version `1`, the chain ID and the DvP contract address. Amounts are integer token units at six decimals. For BUY the trader is the buyer. For SELL the trader is the seller. The signature binds the amounts and addresses, and the contract prevents a buyer/seller pair from settling the same trade ID again.

See the [typed-data fixture](../contracts/fixtures/buy-100.typed-data.json) for a complete example.

## Internal service and metrics

Rust exposes the market snapshot (`/market`), quotes (`/quotes`), the RFQ lifecycle (`/rfqs`), and pending trades (`/trades/pending`). The internal settlement result endpoint trusts Go to verify the chain. Keep these routes on loopback.

Both services expose `GET /healthz` for liveness and `GET /metrics` for HTTP counters and latency histograms. Go adds settlement job gauges, attempts and retries. Metrics use route templates and status classes, rather than per-wallet or per-trade labels.
