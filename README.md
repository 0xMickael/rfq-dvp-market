# rfq-dvp-market

A local trading simulation built with **Rust and Go**. A trader requests buy or sell prices from three simulated market makers and can settle a trade between two fictional assets: a Treasury bill token (`tBILL`) and a euro cash token (`sEUR`).

I built this project to explore market infrastructure while learning Rust and Go. The simulation follows a trade from the request for quote (RFQ) through pricing, inventory reservation and settlement on a local Anvil chain.

## Try it

Use **Windows and PowerShell**. From the repository root, after installing [the required tools](docs/development.md#prerequisites):

```powershell
.\contracts\install-deps.ps1
npm ci --prefix web --ignore-scripts
.\scripts\demo.ps1
```

Open [the trader interface](http://127.0.0.1:5173). Select **BUY**, enter **100 tBILL**, and click **Request quotes** to compare the available prices. You can try this without a wallet.

To settle a trade, connect the [funded MetaMask test account](docs/development.md#metamask), request a fresh quote, then choose **Sign and execute**. The interface shows the settlement status, transaction hash and updated balances. Ctrl+C stops the demo. Each demo starts fresh, so use the [persistent setup](docs/development.md#persistent-market) to keep trades across restarts.

## How it works

- **Rust** prices RFQs, ranks the offers and reserves inventory when a trader accepts. Concurrent traders cannot reserve the same stock twice.
- **Go** verifies the trader's signature, signs for the simulated maker, submits the transaction and checks the chain result.
- **PostgreSQL** saves the market state and settlement jobs in persistent mode so Go and Rust can resume after a restart.
- **Solidity** checks the signed terms and transfers both assets atomically. This is delivery versus payment (DvP).

## Tests and measurements

The [verification suite](docs/development.md#verification) covers concurrent reservations, duplicate requests, lost RPC responses and real Go/Rust process crashes. [GitHub Actions](.github/workflows/verify.yml) runs the full Windows suite and the Go race detector on Linux.

A Go load generator simulates multiple wallets through the same API as the trader interface. The [performance report](docs/performance.md) measures quotes, reservations and completed settlements separately on a single local machine.

## Documentation

| Read this | For |
|---|---|
| [Setup and usage](docs/development.md) | Running the demo, connecting MetaMask, keeping state and running tests |
| [Architecture](docs/architecture.md) | Pricing, concurrent reservations, settlement recovery and design trade-offs |
| [API reference](docs/api.md) | HTTP endpoints and signed trade terms |
| [Performance report](docs/performance.md) | Local results, what they measure and how to reproduce them |

The simulation uses public test accounts and fictional assets on a local chain. It has no order book, partial fills, public deployment or real custody.

## Feedback

This is a learning project. If you have a question, spot an error, or want to suggest a different approach, please [open an issue](https://github.com/0xMickael/rfq-dvp-market/issues).
