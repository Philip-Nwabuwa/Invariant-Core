-- 0001_init (down) — drop everything in reverse dependency order.

DROP TABLE IF EXISTS recon_exceptions;
DROP TABLE IF EXISTS recon_runs;
DROP TABLE IF EXISTS outbox;
DROP TABLE IF EXISTS idempotency_keys;
DROP TABLE IF EXISTS account_balances;

-- entries depends on the balancing/mutation triggers; drop triggers first.
DROP TRIGGER IF EXISTS trg_transaction_balanced ON entries;
DROP TRIGGER IF EXISTS trg_entries_no_update ON entries;
DROP FUNCTION IF EXISTS assert_transaction_balanced();
DROP FUNCTION IF EXISTS entries_no_mutation();

DROP TABLE IF EXISTS entries;
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS accounts;

-- pgcrypto left in place: it may be shared; dropping it is intentionally omitted.
