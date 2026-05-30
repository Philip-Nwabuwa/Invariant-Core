# Product Requirements Document — Invariant Core + Reconcile

**Status:** Draft v1 · **Owner:** (you) · **Last updated:** sprint 0

## 1. Problem

In Nigeria's inter-bank instant-payment system, the failure mode that hurts customers most is not the system being down — it is the system being *inconsistent*. A transfer times out after the sender has been debited, a success callback arrives twice, a fee is applied on one side but not the other, or a reversal is initiated but never actually settles. The money silently ends up in the wrong place, and the customer is left chasing a refund.

The regulatory and commercial stakes are real. The CBN's *Regulation on Instant (Inter-Bank) Electronic Funds Transfer Services* requires a failed NIP transfer to be reversed within 24 hours or it attracts a penalty of ₦10,000 per item, and an inward transfer must be credited within 4 minutes. This sits on top of a system that processed on the order of ₦1.07 quadrillion across 11.2 billion transactions in 2024.

Invariant Core models this end to end: a transfer engine and double-entry ledger that **prevent** inconsistency in real time, and a reconciliation tool that **detects** any inconsistency that slips through, after settlement.

## 2. Goals

- **G1 — No stranded money.** Under adversarial conditions (timeouts, duplicate callbacks, crashes mid-flow), every debit must end in either a matching credit or a completed reversal. This is the headline guarantee.
- **G2 — Provable correctness of the ledger.** The ledger never creates or destroys value; every transaction's debits equal its credits, and this is verifiable by automated test, not assertion.
- **G3 — Trustworthy reconciliation.** Given an internal export and an external settlement file, Reconcile finds 100% of injected discrepancies and categorizes each correctly. Runs are deterministic and re-runnable without double-counting.
- **G4 — Closed loop.** A detected, actionable exception (a pending reversal that never settled) can trigger corrective action back in the switch.
- **G5 — Operability.** Every component is observable (structured logs, metrics, traces) and deploys as a single static binary suitable for low-infrastructure environments.

## 3. Non-goals

- **NG1** — Not a licensed or production payment switch; no ISO 8583 over real network links to a live scheme.
- **NG2** — No real funds, no integration with NIBSS or any bank.
- **NG3** — No real BVN/NIN or other regulated KYC data.
- **NG4** — Not a full fraud-detection system (that is a separate project).
- **NG5** — No customer-facing UI beyond the REST API and the CLI. A web/mobile front end is out of scope for v1.

## 4. Personas

- **The customer (the protected party).** Does not use the system directly, but is the entity whose money must never be stranded. Every requirement traces back to protecting them.
- **The operations analyst.** Runs reconciliation, reads the exceptions report, and chases the discrepancies it surfaces. Cares about clarity, determinism, and being able to trust the matched/unmatched counts.
- **The platform engineer (you).** Operates the services, reads the dashboards and traces, and is on the hook when a debit goes missing. Cares about idempotency, replayability, and observability.

## 5. Functional requirements

### Transfers (switch)
- **FR-T1** — Accept a transfer request over REST with an `Idempotency-Key` header carrying source, destination, amount (in minor units), currency, and a reference.
- **FR-T2** — Reject a duplicate `Idempotency-Key` by returning the original result, not by processing twice.
- **FR-T3** — Drive each transfer through an explicit state machine (see ARCHITECTURE §4) with a single source of externalized state — never in-memory only.
- **FR-T4** — On rail timeout or unknown outcome, treat the transfer as in-doubt and route it to the reversal flow rather than assuming success or failure.
- **FR-T5** — Handle duplicate rail callbacks idempotently; a second "success" for the same transfer is a no-op.

### Ledger
- **FR-L1** — Post a transaction as a set of two or more balanced entries in a single atomic, serializable database transaction.
- **FR-L2** — Reject any transaction whose debits do not equal its credits.
- **FR-L3** — Keep the journal append-only; entries are never updated or deleted. Corrections are new compensating transactions.
- **FR-L4** — Expose account balance as a function of entries (optionally with a cached balance updated in the same transaction).
- **FR-L5** — Provide an export of entries/transactions in the canonical record format for reconciliation.

### Reversals
- **FR-R1** — A reversal is a new transaction that references its parent and posts compensating entries restoring the source.
- **FR-R2** — Reversals are idempotent: re-running a reversal for an already-reversed transaction is a no-op.
- **FR-R3** — Reversal work is durable across crashes via the transactional outbox; a process restart resumes pending reversals.

### Reconciliation
- **FR-C1** — Ingest two inputs: an internal export and an external settlement file, each via a pluggable adapter that normalizes to the canonical record.
- **FR-C2** — Match records on configurable keys (reference, amount, timestamp window) with configurable tolerances.
- **FR-C3** — Categorize every non-matched record as one of: `unmatched_internal`, `unmatched_external`, `amount_mismatch`, `pending_reversal`, `duplicate`.
- **FR-C4** — Produce a human-readable exceptions report and a machine-readable (JSON) artifact.
- **FR-C5** — Record each run (inputs, timestamps, matched count, exception count, summary) so runs are auditable.
- **FR-C6** — Be deterministic: the same two inputs always produce the same result, independent of row order.
- **FR-C7** — Stream large files rather than loading them fully into memory.

### Feedback loop
- **FR-F1** — Emit `pending_reversal` exceptions to the switch (via API or queue) so corrective action can be triggered.

## 6. Non-functional requirements

- **NFR-1 (Correctness over availability).** Where money is concerned, the system prefers to refuse or hold than to act inconsistently. The ledger invariant is non-negotiable.
- **NFR-2 (Latency).** Public transfer API p99 under 250 ms excluding the simulated rail's injected delay.
- **NFR-3 (Throughput target).** Sustain ≥ 500 transfers/sec on a single modest node in load tests (a target to design toward and measure, not a guarantee).
- **NFR-4 (Durability).** No acknowledged transfer is lost across a process restart; in-doubt transfers are recoverable.
- **NFR-5 (Auditability).** Every state transition and every ledger entry is traceable to a transaction and a reference.
- **NFR-6 (Money representation).** All monetary amounts are integer minor units (kobo). Floating-point money is prohibited.
- **NFR-7 (Observability).** Each service exposes Prometheus metrics, emits structured logs with a correlation ID, and participates in distributed traces.
- **NFR-8 (Deployability).** Each service builds to a single static binary and a small container image.
- **NFR-9 (Testability).** The ledger invariant is covered by property-based tests; the no-stranded-debit guarantee is covered by a chaos test.

## 7. Success metrics / acceptance

- **AC-1** — A chaos test that injects random timeouts, duplicate callbacks, and a mid-flow crash runs N transfers and ends with **zero stranded debits** (every debit matched by a credit or reversal). *Maps to G1.*
- **AC-2** — A property-based test over thousands of random transaction sets shows **total system balance is conserved** for every set. *Maps to G2.*
- **AC-3** — A reconciliation fixture with K deliberately injected discrepancies is detected and categorized with **100% recall and correct category labels**. *Maps to G3.*
- **AC-4** — Running the same reconciliation twice yields **identical reports and no double-counted exceptions**. *Maps to G3/FR-C6.*
- **AC-5** — A `pending_reversal` exception triggers a re-reversal in the switch that the next reconciliation run shows as resolved. *Maps to G4.*
- **AC-6** — A k6 run reports the achieved throughput and p99 latency against NFR-2/NFR-3, with a dashboard screenshot. *Maps to G5.*

## 8. Risks & assumptions

- **Over-scoping** is the primary risk. The mitigation is the sprint plan in ROADMAP.md: ship a narrow, correct, well-tested core before adding breadth.
- **Settlement-file realism.** We assume one realistic settlement-file shape (a NIBSS-style report or a clean CSV) is sufficient; the adapter layer is designed to extend, not to handle every format in v1.
- **Determinism vs concurrency.** Reconciliation is concurrent internally but must produce order-independent output; this is an explicit design constraint, not an afterthought.

## 9. Out of scope for v1

Multi-currency FX, a web/mobile front end, real scheme integration, fraud scoring, multi-region deployment, and a notification product. These are candidate follow-ups once the core guarantees hold.
