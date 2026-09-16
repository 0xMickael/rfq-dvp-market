# rfq-dvp-market — Agent Instructions

## 1. Project purpose

rfq-dvp-market is an educational but production-inspired institutional market infrastructure for trading a tokenized security against tokenized cash.

The system models an RFQ-based market in which investors can buy or sell a tokenized T-Bill (`tBILL`) against a tokenized euro (`sEUR`) from several competing market makers.

The primary purpose of the project is not to maximize feature count.

The primary purposes are:

- understand market infrastructure and RFQ market structure;
- learn how Go is used for backend and blockchain infrastructure;
- learn how Rust is used for deterministic, concurrent, stateful systems;
- understand the boundary between off-chain execution and on-chain settlement;
- design a robust system capable of handling concurrency, failures and load;
- produce a project whose architectural decisions can be understood and defended by its author.

AI may write most implementation code.

However, implementation must remain understandable. Avoid unnecessarily clever abstractions or technologies that make the system harder to reason about.

When introducing a non-trivial mechanism, explain why it is required.

---

# 2. Core product

rfq-dvp-market allows a trader to request quotes to buy or sell `tBILL`.

Example:

Trader requests:

BUY 100 tBILL

Several market makers respond:

MM1: ask 98.08
MM2: ask 98.06
MM3: ask 98.10

The best offer is presented to the trader.

If the trader accepts MM2's quote, the system creates a trade:

Buyer: Trader
Seller: MM2
Quantity: 100 tBILL
Price: 98.06 sEUR / tBILL
Cash amount: 9,806 sEUR

The trade is then settled atomically on an EVM chain:

Trader -> 9,806 sEUR -> MM2
Trader <- 100 tBILL <- MM2

The reverse flow must also exist.

A trader can SELL tBILL.

For BUY orders, lower ask prices are better.

For SELL orders, higher bid prices are better.

---

# 3. Assets

## sEUR

`sEUR` is a fictional tokenized cash asset.

Initial implementation:

ERC-20.

It does not represent real money.

## tBILL

`tBILL` is a fictional fungible token representing a tokenized short-term government security.

Initial implementation:

ERC-20.

It is intentionally simpler than a real tokenized fund.

Do not introduce ERC-4626, ERC-7540, NAV subscriptions or asynchronous fund settlement into the initial rfq-dvp-market scope.

---

# 4. Market participants

The system models the following actors.

## Investor / Trader

Requests quotes and buys or sells tBILL.

Initially identified primarily by an EVM wallet.

Later, wallets may belong to a Participant entity such as `Alice Capital`.

## Market Makers

Initially simulate three market makers:

MM1
MM2
MM3

Each market maker has:

- sEUR inventory;
- tBILL inventory;
- target inventory;
- base spread;
- pricing parameters.

Market makers are initially services/bots, not human-facing applications.

## Market Operator

The rfq-dvp-market off-chain infrastructure.

Mainly composed of the Go backend and Rust market engine.

## Settlement Agent

Submits accepted trades for on-chain settlement.

Initially this may be part of the Go backend.

Conceptually it should remain distinct from market matching.

## Cash Issuer

Can initially mint sEUR for test participants.

## Securities Issuer

Can initially mint tBILL for test participants.

---

# 5. Pricing model

Quotes must not simply be randomly generated.

Start with a deliberately simple economic model.

A reference price can be obtained using:

P\_ref = FaceValue / (1 + r \* tau)

where:

FaceValue = redemption value of the tBILL
r = annualized reference rate
tau = remaining maturity in years

Example:

FaceValue = 100
r = 4%
tau = 0.5

P\_ref ≈ 98.04

This model is intentionally simplified.

Each market maker then derives its own mid-price using inventory.

Conceptually:

inventoryDeviation =
(currentInventory - targetInventory) / targetInventory

mid\_i =
referencePrice - lambda\_i \* inventoryDeviation

Then:

bid\_i = mid\_i - spread\_i / 2
ask\_i = mid\_i + spread\_i / 2

Interpretation:

A market maker holding too much tBILL should become more willing to sell it.

A market maker holding too little tBILL should become more willing to acquire it.

Later, pricing may also depend on RFQ size.

Do not implement sophisticated quantitative finance before the simple model works correctly.

---

# 6. Rust responsibility

Rust is the market engine.

It should own the state and logic where consistency, deterministic transitions and concurrency matter.

Typical responsibilities:

- reference market state;
- market-maker inventories;
- RFQs;
- quotes;
- quote expiry;
- quote selection;
- inventory reservations;
- executions;
- state transitions;
- concurrent access to market state;
- prevention of overselling;
- market-level risk checks.

Rust should NOT be added merely because it is faster.

The purpose of Rust in rfq-dvp-market is to learn how strongly typed, explicit state management and safe concurrency can be used in a market engine.

When using concepts such as:

- ownership;
- borrowing;
- Result;
- Option;
- traits;
- Arc;
- Mutex;
- RwLock;
- channels;
- Tokio;

the implementation should remain understandable and their necessity should be documented or explainable.

Avoid unnecessary Rust complexity.

---

# 7. Go responsibility

Go is the operational and orchestration layer.

Typical responsibilities:

- HTTP API;
- WebSocket API;
- request validation;
- interaction with PostgreSQL;
- communication with Rust;
- settlement queue;
- Ethereum JSON-RPC interaction;
- transaction construction;
- transaction submission;
- receipt tracking;
- confirmations;
- retry policies;
- timeout handling;
- idempotency;
- background workers;
- graceful shutdown;
- metrics;
- operational logging.

Go is also used for the load generator.

Important Go concepts should arise naturally from the architecture:

- goroutines;
- channels;
- context.Context;
- interfaces;
- synchronization;
- worker patterns;
- HTTP;
- gRPC;
- database access;
- cancellation;
- retries.

Do not introduce microservices only for architectural appearance.

The initial Go backend may be one modular service.

---

# 8. Go ↔ Rust communication

The exact protocol can initially remain a design decision.

gRPC is a strong candidate.

The boundary should remain conceptually clear:

Go handles external infrastructure and orchestration.

Rust makes market-engine decisions and maintains critical market state.

Avoid unnecessary network boundaries during the earliest prototype if they prevent implementation of the first vertical slice.

---

# 9. Solidity responsibility

Solidity is the trust and settlement layer.

Initial contracts:

SEUR.sol
TBILL.sol
DvPSettlement.sol

The DvP settlement mechanism must ensure atomic exchange:

cash and securities both move, or neither moves.

Later settlement should support signed instructions using EIP-712.

Desired properties include:

- signature verification;
- nonce / replay protection;
- expiry;
- atomic DvP;
- correct token quantities;
- correct counterparties.

The Go settlement submitter must not have arbitrary authority to modify the economic terms accepted by participants.

Do not add unnecessary smart-contract complexity.

---

# 10. Trade lifecycle

The conceptual lifecycle is:

RFQ\_CREATED
-> QUOTING
-> QUOTED
-> ACCEPTED
-> INVENTORY\_RESERVED
-> SETTLEMENT\_PENDING
-> SETTLED

Possible terminal/error states include:

EXPIRED
CANCELLED
REJECTED
SETTLEMENT\_FAILED

State transitions must be explicit.

Invalid transitions must not silently succeed.

Important example:

If settlement permanently fails, reserved market-maker inventory must eventually be released according to a clearly defined recovery policy.

---

# 11. Concurrency requirements

Concurrency is a major learning objective.

Example:

MM2 owns 150 available tBILL.

Alice attempts to buy 100.

Bob simultaneously attempts to buy 100.

The engine must never allow both executions to reserve 200 tBILL.

Required invariant:

availableInventory >= 0

Inventory reservations and executions must be consistent under concurrent requests.

Do not hide concurrency problems using unrealistic assumptions such as "only one request occurs at a time."

---

# 12. Frontend

The frontend should initially remain intentionally simple.

Main trader screen:

- wallet connection;
- participant identity if available;
- sEUR balance;
- tBILL balance;
- BUY / SELL selector;
- quantity;
- Request Quotes;
- received quotes;
- best quote indication;
- quote expiry;
- Execute;
- trade status;
- transaction hash after settlement.

The frontend is not responsible for load testing.

Avoid spending disproportionate project time on visual design.

---

# 13. Operator dashboard

A later operator dashboard should expose operational state such as:

- RFQs received;
- trades executed;
- active quotes;
- settlement queue size;
- pending transactions;
- failed settlements;
- market-maker availability;
- request throughput;
- latency;
- error rate.

This is primarily an observability interface.

---

# 14. Load generator

Create a dedicated Go load generator.

Its role is to simulate many independent traders.

Example conceptual invocation:

loadgen --traders 1000 --duration 60s --concurrency 200

It should be able to generate:

- concurrent RFQs;
- BUY and SELL activity;
- varying quantities;
- accepted and ignored quotes.

It should report metrics including:

- total RFQs;
- total quotes;
- total executions;
- successful requests;
- rejected requests;
- errors;
- throughput;
- p50 latency;
- p95 latency;
- p99 latency.

The load generator measures client-observed performance.

It is distinct from production monitoring.

---

# 15. Monitoring and observability

Later stages should expose application metrics from Go and Rust.

Preferred architecture:

Go / Rust metrics
-> Prometheus
-> Grafana

Logs should be structured and useful for debugging distributed flows.

Every important request should eventually be traceable through a stable identifier such as:

rfq\_id
quote\_id
trade\_id
settlement\_id

Distributed tracing may be considered later but is not required for the first implementation.

---

# 16. Blockchain environments

Development order:

1. Anvil
2. Sepolia

Do not use Ethereum mainnet.

Do not run a full Ethereum node for rfq-dvp-market.

During local development, use Anvil.

During the public test deployment, access Sepolia through an RPC provider.

rfq-dvp-market Go services are blockchain clients.

They are not Ethereum nodes and must not attempt to reproduce Ethereum node functionality.

---

# 17. Robustness goals

The project should intentionally test failure scenarios.

Examples:

- concurrent trader requests;
- market maker inventory exhaustion;
- duplicate HTTP request;
- quote expiry;
- quote accepted at expiration boundary;
- Rust engine unavailable;
- Go restart;
- PostgreSQL temporarily unavailable;
- RPC timeout;
- dropped RPC connection;
- reverted settlement transaction;
- stuck transaction;
- duplicated transaction submission;
- service restart during pending settlement;
- blockchain reorganization later in the project.

For each meaningful failure scenario, document:

failure
expected behavior
recovery mechanism
invariant that must remain true

---

# 18. Important invariants

Examples of invariants that must eventually be tested:

Market-maker inventory must never become negative.

A quote cannot execute after its expiry.

A quote cannot execute more than allowed by its terms.

A trade cannot settle twice.

A settlement cannot exist without a valid accepted trade.

An RFQ identifier must uniquely identify an RFQ.

Economic quantities must remain internally consistent.

Token supply and balances must respect the expected accounting model.

Concurrency must not create inventory out of nothing.

---

# 19. Development phases

Do not attempt to implement the entire roadmap simultaneously.

## Phase 1 — Core Market

Goal:

one complete vertical flow.

Required:

- sEUR;
- tBILL;
- BUY and SELL;
- three market makers;
- simple pricing model;
- RFQ flow;
- Rust market engine;
- Go backend;
- atomic DvP;
- Anvil;
- basic trader frontend.

The highest priority is having one understandable end-to-end flow.

## Phase 2 — Production-like Market

Add progressively:

- PostgreSQL;
- participant model;
- authentication;
- persistence;
- concurrency hardening;
- idempotency;
- queues;
- retries;
- service restart recovery;
- load generator;
- metrics;
- operator dashboard;
- failure testing;
- potentially partial fills.

## Phase 3 — Public Demo

Add:

- Sepolia;
- RPC provider;
- deployment;
- CI/CD;
- hosted frontend/backend;
- Prometheus/Grafana where useful;
- load/performance report;
- architecture documentation;
- threat model;
- failure model.

---

# 20. Explicitly out of scope for now

Do not introduce these without an explicit future decision:

- Ethereum mainnet;
- self-hosted Ethereum full node;
- Kubernetes;
- multi-chain;
- bridges;
- ZK;
- account abstraction;
- solver networks;
- MEV systems;
- order book;
- sophisticated auction mechanisms;
- real KYC;
- real customer data;
- real money;
- real securities;
- production custody;
- advanced quantitative pricing;
- unnecessary microservices.

These ideas may be revisited after the core project is complete.

---

# 21. Proposed repository structure

rfq-dvp-market/
contracts/
market-engine/
backend/
web/
loadgen/
docs/

contracts/
Solidity / Foundry.

market-engine/
Rust.

backend/
Go.

web/
TypeScript frontend.

loadgen/
Go load-testing tool.

docs/
Architecture, market model, lifecycle, trust model, failure model and project decisions.

---

# 22. Documentation expectations

Architecture decisions should be documented.

Important documents should progressively include:

docs/architecture.md
docs/market-model.md
docs/trade-lifecycle.md
docs/trust-model.md
docs/failure-model.md

Documentation should explain why a mechanism exists, not merely repeat implementation details.

---

# 23. AI development rules

The human author is deliberately using AI for implementation.

Do not assume code generation itself is the learning objective.

The learning objective is understanding the architecture and engineering decisions.

When implementing non-trivial mechanisms:

1. prefer understandable solutions;
2. explain important architectural choices;
3. identify relevant trade-offs;
4. avoid unnecessary abstractions;
5. write tests that demonstrate the behavior;
6. clearly state assumptions;
7. point out concurrency or security implications;
8. do not silently expand the project scope.

Do not implement a major architecture change solely because it seems sophisticated.

Before large changes, first inspect the existing architecture.

When proposing a new dependency or technology, explain the problem it solves.

---

# 24. Initial implementation rule

Do not begin by implementing the entire system.

The first goal is to create the project skeleton, architecture documents, build/test environments and a minimal end-to-end plan.

Then implement one vertical slice:

Trader requests BUY 100 tBILL
-> Go receives RFQ
-> Rust obtains/calculates quotes
-> best quote is selected
-> trader accepts
-> inventory is reserved
-> Go submits settlement
-> Solidity atomically exchanges sEUR and tBILL on Anvil
-> status becomes SETTLED

Once this flow is understandable and tested, progressively add the rest of the system.
