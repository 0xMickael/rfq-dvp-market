BEGIN;
CREATE TABLE IF NOT EXISTS schema_version (
    version integer PRIMARY KEY CHECK (version = 1)
);
CREATE TABLE IF NOT EXISTS market_state (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    identity text NOT NULL,
    payload jsonb NOT NULL
);
-- Go owns these tables; it never updates market_state.
CREATE TABLE IF NOT EXISTS settlement_identity (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    identity text NOT NULL
);
CREATE TABLE IF NOT EXISTS acceptance_intents (
    trade_id text PRIMARY KEY,
    rfq_id text NOT NULL,
    payload jsonb NOT NULL,
    completed boolean NOT NULL DEFAULT false
);
CREATE TABLE IF NOT EXISTS settlement_jobs (
    trade_id text PRIMARY KEY REFERENCES acceptance_intents(trade_id),
    payload jsonb NOT NULL,
    submitter_nonce bigint UNIQUE,
    transaction_hash text UNIQUE,
    raw_transaction bytea,
    CHECK ((raw_transaction IS NULL) = (submitter_nonce IS NULL)),
    CHECK ((raw_transaction IS NULL) = (transaction_hash IS NULL))
);
INSERT INTO schema_version VALUES (1) ON CONFLICT DO NOTHING;
COMMIT;
