-- 0001_init — Invariant Core + Reconcile initial schema (PostgreSQL 16).
-- Mirrors db/schema.sql. Money is ALWAYS integer minor units (kobo).

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Accounts: the chart of accounts. Typed so the normal balance direction is meaningful.
CREATE TABLE accounts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code        TEXT NOT NULL UNIQUE,                 -- e.g. CUST-001, SETTLEMENT, FEES
    name        TEXT NOT NULL,
    type        TEXT NOT NULL CHECK (type IN ('asset','liability','equity','revenue','expense')),
    currency    CHAR(3) NOT NULL DEFAULT 'NGN',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Transactions: the journal header. A reversal points at its parent.
CREATE TABLE transactions (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reference             TEXT NOT NULL,
    type                  TEXT NOT NULL CHECK (type IN ('transfer','reversal','fee')),
    status                TEXT NOT NULL CHECK (status IN
                              ('pending','debited','settled','failed','timed_out','reversed')),
    idempotency_key       TEXT UNIQUE,
    parent_transaction_id UUID REFERENCES transactions(id),
    currency              CHAR(3) NOT NULL DEFAULT 'NGN',
    initiated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    settled_at            TIMESTAMPTZ,
    metadata              JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX idx_transactions_reference ON transactions (reference);
CREATE INDEX idx_transactions_status    ON transactions (status);
CREATE INDEX idx_transactions_parent    ON transactions (parent_transaction_id);

-- Entries: append-only journal lines. >= 2 per transaction; debits must equal credits.
CREATE TABLE entries (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id  UUID NOT NULL REFERENCES transactions(id) ON DELETE RESTRICT,
    account_id      UUID NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    direction       TEXT NOT NULL CHECK (direction IN ('debit','credit')),
    amount_minor    BIGINT NOT NULL CHECK (amount_minor > 0),
    currency        CHAR(3) NOT NULL DEFAULT 'NGN',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_entries_transaction ON entries (transaction_id);
CREATE INDEX idx_entries_account     ON entries (account_id);

-- Block mutation of journal lines at the database level (append-only).
CREATE OR REPLACE FUNCTION entries_no_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'entries are append-only; use a compensating transaction';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_entries_no_update BEFORE UPDATE OR DELETE ON entries
    FOR EACH ROW EXECUTE FUNCTION entries_no_mutation();

-- Balancing invariant, enforced at COMMIT via a DEFERRED constraint trigger.
-- NOTE (DESIGN-NOTES #3): sums amount_minor regardless of currency. Correct for
-- single-currency (NGN) v1; revisit when multi-currency leaves the out-of-scope list.
CREATE OR REPLACE FUNCTION assert_transaction_balanced() RETURNS trigger AS $$
DECLARE
    diff BIGINT;
BEGIN
    SELECT COALESCE(SUM(CASE WHEN direction = 'debit'  THEN amount_minor ELSE 0 END), 0)
         - COALESCE(SUM(CASE WHEN direction = 'credit' THEN amount_minor ELSE 0 END), 0)
      INTO diff
      FROM entries
     WHERE transaction_id = NEW.transaction_id;

    IF diff <> 0 THEN
        RAISE EXCEPTION 'transaction % is unbalanced by % minor units', NEW.transaction_id, diff;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER trg_transaction_balanced
    AFTER INSERT ON entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION assert_transaction_balanced();

-- Optional cached balances; updated in the SAME serializable txn as the entries.
CREATE TABLE account_balances (
    account_id    UUID PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    balance_minor BIGINT NOT NULL DEFAULT 0,
    currency      CHAR(3) NOT NULL DEFAULT 'NGN',
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Idempotency keys (switch). Durable record of each request + its result.
CREATE TABLE idempotency_keys (
    key                  TEXT PRIMARY KEY,
    request_fingerprint  TEXT NOT NULL,
    transaction_id       UUID REFERENCES transactions(id),
    response             JSONB,
    status               TEXT NOT NULL CHECK (status IN ('in_progress','succeeded','failed')),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at           TIMESTAMPTZ
);

CREATE INDEX idx_idempotency_expires ON idempotency_keys (expires_at);

-- Transactional outbox. State changes + their follow-up events in one txn.
CREATE TABLE outbox (
    id              BIGSERIAL PRIMARY KEY,
    aggregate_type  TEXT NOT NULL,
    aggregate_id    UUID NOT NULL,
    event_type      TEXT NOT NULL,
    payload         JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at    TIMESTAMPTZ
);

CREATE INDEX idx_outbox_unpublished ON outbox (created_at) WHERE published_at IS NULL;

-- Reconciliation runs + exceptions.
CREATE TABLE recon_runs (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    started_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at      TIMESTAMPTZ,
    internal_source  TEXT NOT NULL,
    external_source  TEXT NOT NULL,
    status           TEXT NOT NULL CHECK (status IN ('running','completed','failed')),
    matched_count    INTEGER NOT NULL DEFAULT 0,
    exception_count  INTEGER NOT NULL DEFAULT 0,
    summary          JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE recon_exceptions (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id           UUID NOT NULL REFERENCES recon_runs(id) ON DELETE CASCADE,
    category         TEXT NOT NULL CHECK (category IN
                         ('unmatched_internal','unmatched_external',
                          'amount_mismatch','pending_reversal','duplicate')),
    reference        TEXT,
    internal_record  JSONB,
    external_record  JSONB,
    delta_minor      BIGINT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_recon_exceptions_run       ON recon_exceptions (run_id);
CREATE INDEX idx_recon_exceptions_category  ON recon_exceptions (category);
CREATE INDEX idx_recon_exceptions_reference ON recon_exceptions (reference);

-- Seed: system accounts. SETTLEMENT is the counterparty/suspense account.
INSERT INTO accounts (code, name, type, currency) VALUES
    ('SETTLEMENT', 'Settlement / suspense', 'liability', 'NGN'),
    ('FEES',       'Fee revenue',           'revenue',   'NGN')
ON CONFLICT (code) DO NOTHING;
