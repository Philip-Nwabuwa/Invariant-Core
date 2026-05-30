# ADR-0005 — One canonical record in `pkg/`

**Status:** Accepted (Sprint 0)

## Context
Reconciliation compares internal truth (the ledger export) against external truth
(a settlement file). These arrive in different, often messy formats. Matching is
only possible if both sides are expressed in one shared shape.

## Decision
A single canonical transaction record (`pkg/canonical.Record`) is the shape both
the ledger export and every settlement-file adapter produce. It lives in `pkg/`
(not `internal/`) precisely because it is the one type that crosses every
boundary. Adapters are the only place that knows about external formats; they map
*into* the canonical record, and nothing downstream of an adapter ever sees a raw
external row.

## Consequences
- External messiness (fixed-width layouts, odd date formats, scheme-specific
  reference fields) is quarantined in adapters.
- Adding a new settlement-file format is a new adapter, not a change to the
  matching engine.
- The record uses `money.Amount` for amounts (ADR-0001) so the contract itself
  forbids float money.
- `metadata` is free-form and is never used as a match key.
