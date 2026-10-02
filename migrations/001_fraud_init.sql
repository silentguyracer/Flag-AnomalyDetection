-- Migration: 001_fraud_init.sql
-- Fraud and Anomaly Flagging Service Schema

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Transaction History for behavioural window calculations
CREATE TABLE IF NOT EXISTS txn_history (
    transaction_id UUID PRIMARY KEY,
    account_id     UUID NOT NULL,
    amount_minor   BIGINT NOT NULL,
    country        TEXT,
    merchant       TEXT NOT NULL,
    channel        TEXT,
    occurred_at    TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS txn_history_acct_time ON txn_history (account_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS txn_history_time ON txn_history (occurred_at DESC);

-- Running baseline per account (Welford on log-amount)
CREATE TABLE IF NOT EXISTS account_profile (
    account_id    UUID PRIMARY KEY,
    n             BIGINT NOT NULL DEFAULT 0,
    mean_log      DOUBLE PRECISION NOT NULL DEFAULT 0,
    m2_log        DOUBLE PRECISION NOT NULL DEFAULT 0,
    countries     JSONB NOT NULL DEFAULT '{}'::jsonb,   -- {"GB": "2026-09-29T...", "FR": "..."}
    last_country  TEXT,
    last_txn_at   TIMESTAMPTZ,
    first_seen_at TIMESTAMPTZ NOT NULL
);

-- Fraud flags emitted by the evaluation engine
CREATE TABLE IF NOT EXISTS flags (
    flag_id        UUID PRIMARY KEY,
    transaction_id UUID NOT NULL UNIQUE,         -- one flag per txn, idempotent
    account_id     UUID NOT NULL,
    score          DOUBLE PRECISION NOT NULL,
    severity       TEXT NOT NULL CHECK (severity IN ('low','medium','high')),
    signals        JSONB NOT NULL,               -- rule, score, reason, evidence
    rules_version  TEXT NOT NULL,
    model_version  TEXT,
    status         TEXT NOT NULL DEFAULT 'open'
                   CHECK (status IN ('open','confirmed_fraud','false_positive')),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    reviewed_at    TIMESTAMPTZ,
    reviewed_by    TEXT
);
CREATE INDEX IF NOT EXISTS idx_flags_status ON flags (status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_flags_account ON flags (account_id, created_at DESC);

-- Every evaluated transaction (flagged or not): historical serving-time training set
CREATE TABLE IF NOT EXISTS feature_log (
    transaction_id UUID PRIMARY KEY,
    features       JSONB NOT NULL,
    rule_score     DOUBLE PRECISION NOT NULL,
    ml_score       DOUBLE PRECISION,
    label          TEXT                          -- filled from reviewer verdicts / simulator ('fraud' / 'legitimate')
);

-- Processed events for exactly-once idempotency across consumer instances
CREATE TABLE IF NOT EXISTS processed_events (
    consumer     TEXT NOT NULL,
    event_id     UUID NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);

-- Transactional outbox for publishing flags reliably
CREATE TABLE IF NOT EXISTS outbox (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    topic        TEXT NOT NULL,
    key          TEXT NOT NULL,
    payload      JSONB NOT NULL,
    headers      JSONB NOT NULL DEFAULT '{}'::jsonb,
    published    BOOLEAN NOT NULL DEFAULT false,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_outbox_pending ON outbox (created_at ASC) WHERE published = false;
