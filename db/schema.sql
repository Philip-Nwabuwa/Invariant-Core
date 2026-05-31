-- Invariant Core + Reconcile — reference schema (PostgreSQL 16)
-- This mirrors migrations/0001_init.up.sql. Money is ALWAYS integer minor units (kobo).
-- gen_random_uuid() is built into PostgreSQL 13+. pgcrypto kept for portability.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ---------------------------------------------------------------------------
-- Accounts: the chart of accounts. Double-entry needs typed accounts so the
-- normal balance direction is meaningful (assets/expenses normal-debit, etc.).
-- ---------------------------------------------------------------------------
CREATE TABLE accounts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code        TEXT NOT NULL UNIQUE,                 -- e.g. CUST-001, SETTLEMENT, FEES
    name        TEXT NOT NULL,
    type        TEXT NOT NULL CHECK (type IN ('asset','liability','equity','revenue','expense')),
    currency    CHAR(3) NOT NULL DEFAULT 'NGN',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Transactions: the logical money movement (the journal header). A reversal
-- points at its parent. idempotency_key is unique so a replayed request can
-- never create a second transaction.
-- ---------------------------------------------------------------------------
CREATE TABLE transactions (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reference             TEXT NOT NULL,              -- cross-system NIP/session reference
    type                  TEXT NOT NULL CHECK (type IN ('transfer','reversal','fee')),
    status                TEXT NOT NULL CHECK (status IN
                              ('pending','debited','settled','failed','timed_out','reversed',
                               'in_doubt','reversal_pending','manual_review')),
    idempotency_key       TEXT UNIQUE,                -- set by the switch on customer-initiated transfers
    parent_transaction_id UUID REFERENCES transactions(id),  -- non-null for reversals
    currency              CHAR(3) NOT NULL DEFAULT 'NGN',
    initiated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    settled_at            TIMESTAMPTZ,
    metadata              JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX idx_transactions_reference ON transactions (reference);
CREATE INDEX idx_transactions_status    ON transactions (status);
CREATE INDEX idx_transactions_parent    ON transactions (parent_transaction_id);

-- At most one reversal per parent transaction: a re-driven reversal is a
-- DB-enforced no-op so double-reversal can never post (idempotent compensation).
CREATE UNIQUE INDEX uq_reversal_per_parent
    ON transactions (parent_transaction_id)
    WHERE type = 'reversal';

-- ---------------------------------------------------------------------------
-- Entries: the append-only journal lines. Each transaction has >= 2 entries
-- whose debits and credits must balance. amount_minor is BIGINT kobo, > 0;
-- direction carries the sign. Entries are NEVER updated or deleted.
-- ---------------------------------------------------------------------------
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

-- ---------------------------------------------------------------------------
-- Balancing invariant, enforced at COMMIT via a DEFERRED constraint trigger.
-- The application also checks this before posting; the trigger is the backstop
-- so an unbalanced transaction can never be committed by any path.
-- ---------------------------------------------------------------------------
-- Currency-aware (DESIGN-NOTES #3): a transaction must balance within EVERY
-- currency group, not merely in summed minor units. The application still
-- rejects mixed-currency sets; this is the DB backstop.
CREATE OR REPLACE FUNCTION assert_transaction_balanced() RETURNS trigger AS $$
DECLARE
    unbalanced_groups BIGINT;
BEGIN
    SELECT COUNT(*)
      INTO unbalanced_groups
      FROM (
        SELECT currency,
               COALESCE(SUM(CASE WHEN direction = 'debit'  THEN amount_minor ELSE 0 END), 0)
             - COALESCE(SUM(CASE WHEN direction = 'credit' THEN amount_minor ELSE 0 END), 0) AS diff
          FROM entries
         WHERE transaction_id = NEW.transaction_id
         GROUP BY currency
      ) g
     WHERE g.diff <> 0;

    IF unbalanced_groups > 0 THEN
        RAISE EXCEPTION 'transaction % is unbalanced within a currency group', NEW.transaction_id;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER trg_transaction_balanced
    AFTER INSERT ON entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION assert_transaction_balanced();

-- ---------------------------------------------------------------------------
-- Optional cached balances. The journal is the source of truth; this is a
-- read optimization updated inside the SAME serializable transaction as the
-- entries. Drop it if you prefer to always derive balances from entries.
-- ---------------------------------------------------------------------------
CREATE TABLE account_balances (
    account_id    UUID PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    balance_minor BIGINT NOT NULL DEFAULT 0,
    currency      CHAR(3) NOT NULL DEFAULT 'NGN',
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Idempotency keys (switch). Durable record of each request + its result, so a
-- replay returns the original outcome instead of re-processing. Redis is only a
-- cache in front of this table.
-- ---------------------------------------------------------------------------
CREATE TABLE idempotency_keys (
    key                  TEXT PRIMARY KEY,
    request_fingerprint  TEXT NOT NULL,               -- hash of the normalized request body
    transaction_id       UUID REFERENCES transactions(id),
    response             JSONB,
    status               TEXT NOT NULL CHECK (status IN ('in_progress','succeeded','failed')),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at           TIMESTAMPTZ
);

CREATE INDEX idx_idempotency_expires ON idempotency_keys (expires_at);

-- ---------------------------------------------------------------------------
-- Transactional outbox. State changes and the events that must follow them are
-- written here in the same DB transaction; a poller publishes unpublished rows.
-- No dual-write window means no stranded debit on crash.
-- ---------------------------------------------------------------------------
CREATE TABLE outbox (
    id              BIGSERIAL PRIMARY KEY,
    aggregate_type  TEXT NOT NULL,                    -- e.g. 'transfer'
    aggregate_id    UUID NOT NULL,
    event_type      TEXT NOT NULL,                    -- e.g. 'transfer.debited', 'reversal.requested'
    payload         JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at    TIMESTAMPTZ,
    -- Delivery bookkeeping: bounded retries with exponential backoff and a
    -- dead-letter flag, so a poison event steps aside instead of head-of-line
    -- blocking newer events.
    attempts        INT         NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error      TEXT,
    dead_letter     BOOLEAN     NOT NULL DEFAULT false
);

-- Partial index: the poller claims rows that are unpublished, not dead-lettered, and due.
CREATE INDEX idx_outbox_unpublished ON outbox (next_attempt_at)
    WHERE published_at IS NULL AND dead_letter = false;

-- ---------------------------------------------------------------------------
-- Reconciliation runs + exceptions. Every run is recorded for auditability;
-- re-running the same inputs is idempotent and does not double-count.
-- ---------------------------------------------------------------------------
CREATE TABLE recon_runs (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    started_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at      TIMESTAMPTZ,
    internal_source  TEXT NOT NULL,                   -- path/identifier of the ledger export
    external_source  TEXT NOT NULL,                   -- path/identifier of the settlement file
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
    internal_record  JSONB,                           -- canonical record from the ledger side
    external_record  JSONB,                           -- canonical record from the settlement side
    delta_minor      BIGINT,                          -- non-null for amount_mismatch
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_recon_exceptions_run       ON recon_exceptions (run_id);
CREATE INDEX idx_recon_exceptions_category  ON recon_exceptions (category);
CREATE INDEX idx_recon_exceptions_reference ON recon_exceptions (reference);

-- ---------------------------------------------------------------------------
-- Seed: system accounts. Customer accounts are created by scripts/seed.
-- SETTLEMENT acts as the counterparty/suspense account in this simulation.
-- ---------------------------------------------------------------------------
INSERT INTO accounts (code, name, type, currency) VALUES
    ('SETTLEMENT', 'Settlement / suspense', 'liability', 'NGN'),
    ('FEES',       'Fee revenue',           'revenue',   'NGN')
ON CONFLICT (code) DO NOTHING;
