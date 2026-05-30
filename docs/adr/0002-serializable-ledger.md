# ADR-0002 — Serializable ledger writes

**Status:** Accepted (Sprint 0)

## Context
The ledger's hard invariant is that every transaction's debits equal its credits
and that balances are never corrupted by concurrent posting. Weaker isolation
levels admit anomalies (e.g. read skew on a cached balance) that can silently
violate conservation.

## Decision
`PostTransaction` runs at PostgreSQL `SERIALIZABLE` isolation. The balanced-entry
check and the optional cached-balance update happen inside that same transaction.
Correctness of balances is preferred over raw write throughput on the ledger path.

## Consequences
- Serialization failures (`40001`) are expected under contention and must be
  retried by the caller; this retry path is implemented on the ledger write.
- A shared suspense account (`SETTLEMENT`) is a contention hotspot: every transfer
  serializes on its cached-balance row (DESIGN-NOTES #2). The
  **serialization-retry rate on system accounts** is therefore a first-class SLI
  alongside outbox lag, and it — not Postgres itself — determines whether the
  NFR-3 throughput target is reachable.
- Mitigations held in reserve (apply only if k6 forces it): shard the suspense
  account into N sub-accounts by hash, or drop the cached balance for hot system
  accounts and derive on read. Do not pre-optimize.
