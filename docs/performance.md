# Local performance measurements

The Go load generator measures three paths through the same API used by the trader interface. These results describe a short local experiment on one machine. The [raw JSON](performance-results.json) includes the trial settings, individual reports and binary hashes.

## What each campaign exercises

| Campaign | Timed client journey |
|---|---|
| Quotes | Request and receive prices |
| Reservations | Request a quote, sign with a software wallet, and try to reserve inventory. The settlement worker is paused. |
| Full settlement | Run the signed trade and poll until the application reports a terminal outcome |

Concurrency 8 means up to eight client journeys in flight at once, with at most one per wallet. In full settlement, waiting for the final status occupies a slot. The load generator uses Go's elapsed-time clock. Human interaction and MetaMask confirmation time are outside the measurement.

## Reference results

Recorded on 16–17 November 2026 on a Windows laptop with an Intel Core i7-8550U, native PostgreSQL 18.7 with durable writes, a release Rust engine, optimized Go binaries and Anvil automining. All components share the machine.

Each mode ran three times at concurrency 1 and 8, for 18 reference trials. Every trial used a fresh database and chain, eight deterministic wallets and seed 42. The generator ran a one-second quote warmup before admitting measured operations for three seconds, then waited for in-flight operations to finish. Setup, warmup and the final audit are outside the timed window.

| Campaign | Concurrency | Median completed ops/s | RFQ p95 (ms) | Acceptance to terminal p95 (ms) |
|---|---:|---:|---:|---:|
| Quotes | 1 | 35.76 | 48.41 | — |
| Quotes | 8 | 41.03 | 241.71 | — |
| Reservations | 1 | 98.89 | 12.37 | — |
| Reservations | 8 | 59.84 | 98.26 | — |
| Full settlement | 1 | 0.52 | 13.92 | 1926.05 |
| Full settlement | 8 | 3.82 | 117.74 | 2008.87 |

Rates divide all completed operations by the elapsed time through the last completion, including drain. Each reported value is the median of three trial values. Percentiles use nearest rank within each trial. The JSON includes p50, p95 and p99 for all responses and successful responses.

The reservation campaign asks for 600–700 tBILL at a time. It quickly exhausts inventory and yields six successful reservations in each trial, so most completions are business rejections. Quotes and full settlement use 0.01–0.1 tBILL. Each full-settlement journey in these trials settled successfully, so its completion rate also counts settled trades.

## Correctness and interpretation

All 18 reference trials passed the final checks without technical errors. They produced 54 settlements and 36 reservations that were intentionally left open. The audit checks identifiers, exact signed terms and inventory accounting. For settled trades it also checks exact token balances, unchanged supplies and one matching DvP event per trade. The raw JSON also includes a separate mixed-acceptance trial, excluded from the table.

With eight concurrent clients, the quote campaign completed more requests per second and observed longer RFQ latency. The global Rust lock and snapshot writes are candidates for profiling. This benchmark does not isolate their cost. The settlement worker and client polling contribute to the roughly two-second settlement p95. With only two or sixteen settlements per trial, that percentile often equals the maximum, so the experiment gives little information about the tail under sustained load.

The [benchmark commands](../loadgen/README.md) reproduce the workload. The CI runs smaller campaigns to check correctness and does not enforce these timings.
