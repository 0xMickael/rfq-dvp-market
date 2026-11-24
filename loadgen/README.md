# Run the load generator

This Go CLI simulates several trader wallets through the normal API. It can measure RFQs, signed inventory reservations or full settlement. The [performance report](../docs/performance.md) explains the workload, timings and correctness audit.

## Reproduce the reference workload

Follow [the setup guide](../docs/development.md#prerequisites) and install project-local PostgreSQL with `.\scripts\install-postgres.ps1`. Build the three executables once from the repository root in PowerShell:

```powershell
New-Item -ItemType Directory -Force .cache | Out-Null

Push-Location backend
go build -o ../.cache/durable-api.exe ./cmd/rfq-dvp-market-api
Pop-Location

Push-Location market-engine
cargo build --release --locked --target-dir ../.cache/loadgen-rust
Pop-Location

Push-Location loadgen
go build -o ../.cache/loadgen.exe .
Pop-Location
```

Run the campaigns:

```powershell
.\scripts\benchmark.ps1 -Repetitions 3 -Concurrency 1,8 -Traders 8 -DurationSeconds 3 -WarmupSeconds 1 -Requests 10000 -SettlementRequests 100
```

The script starts an isolated PostgreSQL cluster and creates a fresh database, Anvil chain and application processes for each trial. It uses ports 38080, 38081, 38545 and 55435. It shuts down its own processes and keeps reports and logs under `.cache/performance/<run>/`.

Use `-Modes quotes`, `-Modes accept` or `-Modes settlement` to select a campaign. Use `Get-Help .\scripts\benchmark.ps1 -Full` for script parameters.

## Run against an existing local market

The standalone executable accepts `-help`. Before running acceptance or settlement, prepare the selected test wallets with the same trader count and seed. Preparation funds them and sets token allowances. Running preparation again adds funds.

```powershell
$market = @('-api', 'http://127.0.0.1:8080', '-rpc', 'http://127.0.0.1:8545')
.\.cache\loadgen.exe @market -prepare -traders 16 -seed 42
.\.cache\loadgen.exe @market -mode settlement -traders 16 -concurrency 8 -duration 10s -requests 100 -seed 42
```

Use a disposable Anvil deployment with chain ID 31337. The CLI uses deterministic software wallets and requires loopback URLs. It reports business rejections separately from technical errors. Technical errors or failed invariants produce a nonzero exit code.
