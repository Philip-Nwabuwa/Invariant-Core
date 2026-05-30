# ADR-0004 — Transactional outbox

**Status:** Accepted (Sprint 0); implemented in Sprint 3 (NS-301)

## Context
A state change in the switch (e.g. a committed debit) and the follow-up it
requires (a rail call, a reversal) must not be split by a crash. A naive
dual-write — commit to the DB, then publish an event — has a window where the DB
commit lands but the publish is lost, stranding a debit with no follow-up. That
window is exactly the failure this system exists to prevent.

## Decision
State changes and the events that must follow them are written in one DB
transaction to an `outbox` table. A poller reads unpublished rows and publishes
them, marking them published. There is no dual-write and therefore no lost-event
window.

## Consequences
- Delivery is at-least-once, so every consumer (rail caller, reversal handler,
  duplicate callbacks) must be idempotent.
- Crash recovery is automatic: on restart the poller resumes unpublished rows;
  this is the machinery that makes "no stranded debit" survive a mid-flow crash
  (AC-1) and that resolves a stranded `in_progress` idempotency key (ADR-0003).
- Outbox lag is a first-class SLI (NS-308).
- A broker (e.g. NATS) is deferred; start with the DB-backed poller and add one
  only if needed.
