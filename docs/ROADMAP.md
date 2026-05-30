# Roadmap — Invariant Core + Reconcile

A 12-week build in seven sprints (a sprint 0 plus six two-week sprints). The order is deliberate: correctness foundation first, then the prevention guarantee, then detection, then the loop. Each sprint has a goal, a small backlog with story IDs, and a definition of done. Portfolio checkpoints at the end tie the work back to the actual objective — credible mid-senior LinkedIn material.

## Milestone summary

| Sprint | Weeks | Theme | Headline outcome |
|---|---|---|---|
| 0 | 0 | Walking skeleton | Repo, tooling, CI, schema, the contract — it compiles and boots. |
| 1 | 1–2 | Ledger core | Provably correct double-entry ledger. |
| 2 | 3–4 | Switch MVP | Happy-path transfer, end to end, idempotent. |
| 3 | 5–6 | Reversals + resilience | Zero stranded debits under chaos. |
| 4 | 7–8 | Reconcile CLI | All exception categories, deterministic, recorded runs. |
| 5 | 9–10 | Feedback + hardening | Closed loop; load-tested; integration-tested. |
| 6 | 11–12 | Polish + portfolio | Dashboards, demo, write-ups, README. |

---

## Sprint 0 — Walking skeleton (week 0)
**Goal:** everything is wired and a no-op request flows through the stack. No business logic yet.

- **NS-001** Initialize module, `Makefile`, `.golangci.yml`, `docker-compose.yml` (postgres/redis/jaeger), `.env.example`.
- **NS-002** Write `db/schema.sql` and the matching `golang-migrate` migration; `make migrate-up` works.
- **NS-003** Define `pkg/canonical/transaction.go` (the contract) and `pkg/money/money.go` (int64 kobo).
- **NS-004** Stand up `pkg/logging`, `pkg/metrics`, `pkg/tracing` and a health endpoint per service.
- **NS-005** Scaffold the four `cmd/` entrypoints so each boots and serves `/healthz`.
- **NS-006** GitHub Actions: lint + `go test ./...` on PR.
- **NS-007** Write ADR-0001…0005 stubs.

**DoD:** `make dev && make migrate-up` succeeds; all four binaries build and pass health checks; CI is green.

## Sprint 1 — Ledger core (weeks 1–2)
**Goal:** a double-entry ledger you can *prove* never creates or destroys money.

- **NS-101** `accounts` + `entries` + `transactions` repositories via sqlc/pgx.
- **NS-102** `PostTransaction` at SERIALIZABLE isolation; reject unbalanced entry sets (FR-L1/L2).
- **NS-103** `GetBalance` derived from entries; optional cached `account_balances` updated in the same txn (FR-L4).
- **NS-104** Append-only enforcement: no update/delete paths on entries (FR-L3).
- **NS-105** Property-based test: conservation of balance across random transaction sets (AC-2).
- **NS-106** ledger gRPC surface + `ExportTransactions` emitting canonical records (FR-L5).

**DoD:** AC-2 passes; balances reconstructible purely from the journal; export produces valid canonical records.

## Sprint 2 — Switch MVP (weeks 3–4)
**Goal:** a real transfer goes in over REST and money moves, once.

- **NS-201** REST `POST /v1/transfers` with `Idempotency-Key` (FR-T1).
- **NS-202** Durable idempotency store (Postgres + Redis fast-path); replay returns stored result (FR-T2, ADR-0003).
- **NS-203** Implement the state machine `INITIATED → … → SETTLED` for the happy path (FR-T3).
- **NS-204** `mockrail` v1: success path with configurable latency.
- **NS-205** switch → ledger debit/credit on a successful transfer.
- **NS-206** correlation-id propagation + a full transfer trace in Jaeger.

**DoD:** an end-to-end happy-path transfer settles; a duplicate idempotency key is a no-op; the trace spans switch → rail → ledger.

## Sprint 3 — Reversals + resilience (weeks 5–6)
**Goal:** the headline guarantee. No debit is ever left stranded.

- **NS-301** Transactional outbox: writer + poller (FR-R3, ADR-0004).
- **NS-302** Reversal as a parent-linked compensating transaction (FR-R1), idempotent (FR-R2).
- **NS-303** In-doubt handling: rail timeout/unknown routes to `REVERSAL_PENDING` (FR-T4).
- **NS-304** Idempotent duplicate rail callbacks (FR-T5).
- **NS-305** `mockrail` chaos: timeout, duplicate-success, decline, all seedable.
- **NS-306** Crash-recovery: kill switchd mid-flow; poller resumes on restart.
- **NS-307** Chaos test asserting zero stranded debits over N transfers (AC-1).
- **NS-308** Metrics: outcomes by terminal state, reversal-latency histogram, outbox lag.

**DoD:** AC-1 passes; a mid-flow crash leaves no stranded debit after restart; dashboards show the outcome split.

## Sprint 4 — Reconcile CLI (weeks 7–8)
**Goal:** prove, after the fact, that internal and external truth agree — and find every gap.

- **NS-401** Cobra CLI skeleton; config via Viper (flags/env).
- **NS-402** Adapters: ledger-export reader + a NIBSS-style settlement reader, both → canonical (FR-C1).
- **NS-403** Streaming matcher keyed on `reference` with amount/timestamp tolerances (FR-C2, FR-C7).
- **NS-404** All five exception categories (FR-C3) incl. `pending_reversal`.
- **NS-405** Text + JSON report (FR-C4); `recon_runs` + `recon_exceptions` persisted (FR-C5).
- **NS-406** Determinism: order-independent output, idempotent re-runs (FR-C6, AC-4).
- **NS-407** `scripts/gen_settlement`: produce a file with K injected discrepancies.
- **NS-408** Fixture test: 100% recall + correct categories (AC-3).

**DoD:** AC-3 and AC-4 pass; a large generated file reconciles in seconds; reports are reproducible.

## Sprint 5 — Feedback + hardening (weeks 9–10)
**Goal:** close the loop and prove the system holds under load.

- **NS-501** Reconcile emits `pending_reversal` exceptions to switchd (FR-F1).
- **NS-502** switchd corrective endpoint triggers a re-reversal; next run shows it resolved (AC-5).
- **NS-503** testcontainers integration suite: serializable posting, idempotent replays, reversal recovery.
- **NS-504** k6 load test; tune to the throughput/latency targets; capture numbers (AC-6, NFR-2/3).
- **NS-505** Backpressure / serialization-failure retry handling on the ledger path.
- **NS-506** Error taxonomy + structured error responses on the REST API.

**DoD:** AC-5 and AC-6 pass; integration suite green in CI; load numbers recorded with a dashboard screenshot.

## Sprint 6 — Polish + portfolio (weeks 11–12)
**Goal:** make it legible to a reader and turn it into portfolio signal.

- **NS-601** Finalize README with the architecture diagram and a "failure modes" section.
- **NS-602** Grafana dashboards committed under `deployments/`.
- **NS-603** A scripted demo: fire transfers under chaos, show zero stranded debits, run reconcile, trigger a re-reversal.
- **NS-604** Complete the ADRs.
- **NS-605** Write the build-log posts (see checkpoints).
- **NS-606** (Optional, deferred) spin up a *separate* breadth track — USSD engine or offline-sync — rather than bloating this repo.

**DoD:** a newcomer can clone, `make dev`, and run the demo from the README in under 15 minutes.

---

## Portfolio checkpoints (tie back to the goal)

Post a short build-log entry at the end of each sprint — the series outperforms one launch post and shows sustained depth:

- **After Sprint 1:** "How I made a ledger that *can't* lose a kobo — property-based testing the conservation invariant." (artifact: the failing-then-passing property test)
- **After Sprint 3:** "₦52bn-scale systems live or die on consistency, not uptime. Here's the in-doubt state and the reversal that stops a debit stranding a trader's money." (artifact: the chaos-test dashboard)
- **After Sprint 4:** "The least glamorous code in fintech decides whether you get your money back. A reconciliation engine that finds the silent mismatches." (artifact: an exceptions report)
- **After Sprint 5:** "Detection feeding correction: when reconciliation finds a pending reversal, the switch fixes it automatically." (artifact: the before/after reconcile runs)

Each post leads with the Nigerian number and the engineering decision, names the alternative you rejected, and ends with a real question for other engineers.

## Scope guardrails

- A narrow, correct, well-tested core beats a broad, shaky one. If a sprint slips, cut breadth (NS-606, extra adapters) before cutting tests.
- Resist building every settlement-file format. One realistic shape + an extensible adapter layer is the v1 bar.
- Keep money integer, keep the journal append-only, keep reconciliation deterministic — these three are non-negotiable regardless of time pressure.
