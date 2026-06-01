# ADR-0003 — Durable idempotency keys

**Status:** Accepted (Sprint 0); replay contract finalized in Sprint 2 (NS-202)

## Context
A retried transfer request must never move money twice. Network retries, client
timeouts, and at-least-once delivery all produce duplicate requests carrying the
same `Idempotency-Key`.

## Decision
Each `Idempotency-Key` is stored durably in Postgres (`idempotency_keys`) with a
request fingerprint and the produced result. A replay returns the stored result
instead of reprocessing. Redis is only a fast-path cache in front of this table,
never the record of truth — a replay after a cache flush is still correct.

## Consequences / replay contract
- The transfer's durable state machine (in Postgres) is the source of truth, not
  the idempotency row (ties to ADR-0004).
- **`in_progress` replay contract** (DESIGN-NOTES #5), as specified with the store
  in NS-202:
  - A replay within the lease window (`expires_at` not passed) returns
    "in progress, retry later".
  - A replay past the lease re-attaches to the existing `transaction_id` and lets
    the outbox poller drive it to a terminal state, then returns that result.
  - A replay never starts a second transfer: the key is unique and already bound
    to a `transaction_id`.
- Lease duration and the exact replay response shape were defined in NS-202.
