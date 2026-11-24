# Setup and usage

Run these commands from the repository root in Windows PowerShell. The launchers use native Windows tools and a local Anvil chain.

## Prerequisites

Install Go, Rust via `rustup`, Node.js with npm, and Foundry (`forge`, `anvil` and `cast`). On Windows, Rust also needs the MSVC C++ Build Tools and Windows SDK.

Install the repository dependencies once per checkout:

```powershell
.\contracts\install-deps.ps1
npm ci --prefix web --ignore-scripts
```

## Fresh demo

```powershell
.\scripts\demo.ps1
```

The script builds the application, starts Anvil, deploys the contracts and funds the test wallets. Once the terminal reports readiness, open [127.0.0.1:5173](http://127.0.0.1:5173). You can request indicative quotes immediately. To execute a trade, connect the test wallet described below and request a fresh quote.

Keep the terminal open. Ctrl+C stops the demo. Each run starts with fresh balances and market state. Startup and application logs are under `.cache/demo/`.

## MetaMask

The deployment funds and approves token spending for the default Anvil trader at account index **1**, address `0x70997970C51812dc3A010C7d01b50e0d17dc79C8`. Derive its public test private key locally with Foundry:

```powershell
cast wallet private-key 'test test test test test test test test test test test junk' 1
```

1. Import that account into MetaMask. Use Anvil test accounts only on the local chain.
2. In the trader interface, select **Connect wallet** and approve **Anvil 31337**. The local RPC URL is `http://127.0.0.1:8545`.
3. Request a BUY or SELL quote. You can enter a security quantity in tBILL or a cash amount in sEUR. For BUY, cash is a maximum budget. For SELL, it is minimum proceeds.
4. Select **Sign and execute** on a quote before it expires. MetaMask signs the trade terms. The Go submitter pays gas and sends the transaction.
5. Wait for `SETTLED`, then check the transaction hash and both wallet balances.

The other preset accounts act as deployer and issuer (index 0), market makers (2–4) and settlement submitter (5). The deployment script handles their funding and approvals.

## Persistent market

For your first setup, run:

```powershell
.\scripts\install-postgres.ps1
.\scripts\postgres.ps1 -Action Init
.\scripts\postgres.ps1 -Action Start
.\scripts\postgres.ps1 -Action Migrate
.\scripts\local-chain.ps1 -Initialize
```

Keep the chain terminal open. In a second terminal run:

```powershell
.\scripts\start.ps1
```

Open the same [trader interface](http://127.0.0.1:5173). Ctrl+C in the application terminal stops Go, Rust and the frontend. Restart `start.ps1` against the existing chain and database to resume. If you stopped all services, run `postgres.ps1 -Action Start` and `local-chain.ps1` without `-Initialize` first. Stop PostgreSQL with `.\scripts\postgres.ps1 -Action Stop`.

The installer verifies the archive checksum and extracts binaries under `.cache/tools/`. It requires no Windows service. The demo database is stored in `.cache/postgres/demo/`, and Anvil saves its chain under `.cache/local-market/`. PostgreSQL uses trusted loopback connections for this local setup. Keep both data directories together. The periodic chain dump and database are separate writes, so they do not form an atomic backup.

## Verification

After installing the repository dependencies, use the [versions pinned for CI](../scripts/toolchains.json) to run the full suite:

```powershell
.\scripts\install-postgres.ps1
.\scripts\verify.ps1
```

This runs tests, formatting and static checks, builds, and real-process tests for signatures, settlement and recovery. It also runs a small load campaign to check correctness. `-SkipIntegration` skips the real-process and load checks. GitHub Actions runs the full suite on Windows and Go race tests on Linux.

| Task | Entry point |
|---|---|
| Start a fresh interactive market | `scripts/demo.ps1` |
| Install PostgreSQL and manage its dedicated cluster | `scripts/install-postgres.ps1` and `scripts/postgres.ps1` |
| Run the persistent chain and application | `scripts/local-chain.ps1` and `scripts/start.ps1` |
| Run the same checks as Windows CI | `scripts/verify.ps1`, which invokes `test-integration.ps1` and `test-recovery.ps1` |
| Reproduce load measurements | [Benchmark commands](../loadgen/README.md) |

Verification reports are written to `.cache/verification/<run>/`. The integration and benchmark runners use their own ports and state directories.

## Troubleshooting

- **Startup reports a busy port.** Stop the existing process before retrying. The interactive setup uses 5173 for the UI, 8080 for Go, 8081 for Rust, 8545 for Anvil and 55432 for PostgreSQL.
- **MetaMask shows no funded balance.** Check the address and chain ID above, then confirm the chain is running. Start a new quote after switching networks.
- **Persistent startup reports a readiness error.** Read [the readiness response](http://127.0.0.1:8080/api/readiness) and application logs under `.cache/start/`. After a database outage, restore PostgreSQL before restarting the application. Inventory mismatches require checking that the chain and database belong to the same market.
