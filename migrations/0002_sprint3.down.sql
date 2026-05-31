-- 0002_sprint3 (down) — reverse the Sprint 3 schema in dependency order.

-- Restore the currency-agnostic balance check (single summed diff).
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

-- Restore the original outbox index + drop the delivery-bookkeeping columns.
DROP INDEX idx_outbox_unpublished;
ALTER TABLE outbox
    DROP COLUMN attempts,
    DROP COLUMN next_attempt_at,
    DROP COLUMN last_error,
    DROP COLUMN dead_letter;
CREATE INDEX idx_outbox_unpublished ON outbox (created_at) WHERE published_at IS NULL;

DROP INDEX uq_reversal_per_parent;

-- Shrink statuses back to the Sprint 0-2 set.
ALTER TABLE transactions DROP CONSTRAINT transactions_status_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_status_check CHECK (status IN
    ('pending','debited','settled','failed','timed_out','reversed'));
