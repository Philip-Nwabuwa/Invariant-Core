-- 0002_sprint3 — reversals + resilience (Sprint 3). Mirrors db/schema.sql.

-- Grow the transfer lifecycle with the distinct in-doubt / reversal-pending /
-- manual-review states. They are persisted distinctly (no coarse-status overload)
-- so a crashed transfer resumes at its true fine state — a transfer that crashed
-- in doubt comes back in_doubt and re-issues a TSQ, never reverses unconfirmed.
ALTER TABLE transactions DROP CONSTRAINT transactions_status_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_status_check CHECK (status IN
    ('pending','debited','settled','failed','timed_out','reversed',
     'in_doubt','reversal_pending','manual_review'));

-- At most one reversal per parent transaction: a re-driven reversal becomes a
-- DB-enforced no-op (idempotent compensation; double-reversal can never post).
CREATE UNIQUE INDEX uq_reversal_per_parent
    ON transactions (parent_transaction_id)
    WHERE type = 'reversal';

-- Outbox delivery bookkeeping: bounded retries with exponential backoff and a
-- dead-letter flag, so a poison event backs off and steps aside instead of
-- head-of-line blocking newer events.
ALTER TABLE outbox
    ADD COLUMN attempts        INT         NOT NULL DEFAULT 0,
    ADD COLUMN next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN last_error      TEXT,
    ADD COLUMN dead_letter     BOOLEAN     NOT NULL DEFAULT false;

-- The poller claims rows that are unpublished, not dead-lettered, and due.
DROP INDEX idx_outbox_unpublished;
CREATE INDEX idx_outbox_unpublished ON outbox (next_attempt_at)
    WHERE published_at IS NULL AND dead_letter = false;

-- Balance invariant is now currency-aware (DESIGN-NOTES #3): a transaction must
-- balance within EVERY currency group, not merely in summed minor units. The
-- application still rejects mixed-currency sets; this is the DB backstop.
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
