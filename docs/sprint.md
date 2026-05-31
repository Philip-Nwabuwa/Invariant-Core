# Sprint 0 — Walking Skeleton · Task Tracker

Source of truth for Sprint 0 progress. Work **one task at a time**: implement → verify → tick the box → commit → next. No batching.

**Goal:** everything wired so a no-op request flows through the stack, no business logic.
**DoD:** `make dev && make migrate-up` succeeds; all four binaries build and pass `/healthz`; CI green.

**Decisions:** module path `github.com/Philip-Nwabuwa/Invariant-Core` (remote: https://github.com/Philip-Nwabuwa/Invariant-Core.git) · ports — ledger gRPC `:50051`/health `:8081`, switchd REST+health `:8080`/gRPC `:50052`, mockrail `:50053`/health `:8082`, reconcile CLI.

---

## Pre-flight
- [x] Install Go 1.22+, `golang-migrate`, `sqlc`, `buf`, `golangci-lint` via Homebrew; add a `make tools` target for the Go-based ones.
- [x] Add root `.gitignore` (`bin/`, `.env`, `out/`, coverage).
- [x] Confirm module path and the port table.

## NS-001 · Module + tooling + local stack
- [x] `go mod init github.com/Philip-Nwabuwa/Invariant-Core`.
- [x] Move `docs/Makefile` → root `Makefile`.
- [x] Move `docs/docker-compose.yml` → root `docker-compose.yml`.
- [x] Add `.golangci.yml` (v2 schema: defaults + revive; gofumpt/goimports formatters).
- [x] Add `.env.example` (`DB_URL`, per-service ports, `OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317`, `REDIS_ADDR`, mockrail latency/timeout/dup/decline knobs).

## NS-002 · Schema + migration
- [x] Move `docs/schema.sql` → `db/schema.sql`.
- [x] Write `migrations/0001_init.up.sql` mirroring `db/schema.sql` (tables, triggers, indexes, system-account seed).
- [x] Write `migrations/0001_init.down.sql` (drop in reverse dep order: triggers/functions → tables → extension).
- [x] Verify `make dev && make migrate-up`; confirm tables + seeded SETTLEMENT/FEES.

## NS-003 · The contract + money (`pkg/`)
- [x] `pkg/canonical/transaction.go` — `Record` struct + `Type`/`Status` enums per ARCHITECTURE §3.
- [x] `pkg/money/money.go` — `Amount` (int64 kobo): constructors, `Add`/`Sub`/`Neg`, boundary `String()`, no floats (ADR-0001).
- [x] `pkg/money/money_test.go` — table-driven unit tests.

## NS-004 · Observability libs + health (`pkg/`)
- [x] `pkg/logging/logging.go` — `slog` JSON handler + `correlation_id` propagation helper.
- [x] `pkg/metrics/metrics.go` — Prometheus registry + `promhttp` handler builder.
- [x] `pkg/tracing/tracing.go` — OTel tracer provider → OTLP/Jaeger (`:4317`); no-op when endpoint unset.
- [x] `pkg/health` — tiny HTTP server mounting `/healthz` (200 JSON) + `/metrics`.

## NS-005 · Four `cmd/` entrypoints (boot + `/healthz`, no logic)
- [x] `cmd/ledger/main.go` — config + obs init, health `:8081`, gRPC bind `:50051` (empty surface), graceful shutdown. (Shared `internal/serviceboot` helper.)
- [x] `cmd/switchd/main.go` — same wiring; REST + health `:8080`, gRPC bind `:50052`.
- [x] `cmd/mockrail/main.go` — same wiring; health `:8082`, primary listener `:50053`.
- [x] `cmd/reconcile/main.go` — Cobra root + `run` subcommand stub (Viper `--internal/--external`), prints "not implemented".
- [x] Codegen config stubs: `buf.yaml` + `buf.gen.yaml` + minimal `api/proto/{ledger,switch}/v1/*.proto` (buf lint/build green); `sqlc.yaml` (schema parses; queries land Sprint 1).
- [x] `scripts/seed/main.go` + `scripts/gen_settlement/main.go` as compiling stubs.

## NS-006 · CI
- [x] `.github/workflows/ci.yml` — on PR: `setup-go@v5` (1.23), `golangci-lint run`, `go build ./...`, `go test ./... -race`. Lint + tests verified green locally.

## NS-007 · ADR stubs + diagram
- [x] `docs/adr/0001-integer-money.md` … `0005-canonical-record.md` — context→decision→consequences stubs (0002 names the suspense-account retry SLI; 0003 reserves the `in_progress` replay contract).
- [x] `docs/diagrams/data-flow.svg` — render the ARCHITECTURE §1 ASCII flow (fixes broken image in `README.md:13` / `ARCHITECTURE.md:7`).

---

## Verification (Sprint 0 DoD) — ✅ PASSED 2026-05-30
1. [x] `make tools` → `make dev` — pg/redis/jaeger up; postgres+redis healthy.
2. [x] `make migrate-up` — all 8 tables present; SETTLEMENT + FEES seeded; re-run idempotent ("no change").
3. [x] `make build` — `ledger`, `switchd`, `mockrail`, `reconcile` in `./bin`.
4. [x] `/healthz` on `:8081`/`:8080`/`:8082` → 200 (3/3, confirmed on repeat run); `:8080/metrics` serves Prometheus text.
5. [x] `go test ./... -race` green (`pkg/money` ok).
6. [x] `make lint` clean (0 issues).
7. [x] Jaeger UI `:16686` → http 200.
8. [ ] CI green on a PR — workflow added; lint+test (exactly what CI runs) verified green locally. Pending first push/PR.

---

# Sprint 1 — Ledger core · Task Tracker

Source of truth for Sprint 1 progress. Same rule: implement → verify → tick the box → commit → next. No batching.

**Goal:** a double-entry ledger you can *prove* never creates or destroys money.
**DoD:** AC-2 passes; balances reconstructible purely from the journal; `ExportTransactions` produces valid canonical records.

**Decisions:** sqlc package `ledgerdb` → `internal/ledger/postgres/ledgerdb`, queries in `internal/ledger/postgres/queries`, `pgx/v5` pool · ledger writes at `SERIALIZABLE` with a bounded retry on SQLSTATE `40001` (ADR-0002) · domain layer in `internal/ledger`, gRPC server in `cmd/ledger` on `:50051` (replaces the Sprint-0 Ping-only surface) · property lib `pgregory.net/rapid`.

## NS-101 · Repositories — `accounts` + `entries` + `transactions` (sqlc/pgx)
- [x] `internal/ledger/postgres/queries/accounts.sql` — `CreateAccount`, `GetAccountByCode`, `GetAccountByID`.
- [x] `internal/ledger/postgres/queries/transactions.sql` — `InsertTransaction`, `GetTransaction`, `ListTransactionsByWindow`.
- [x] `internal/ledger/postgres/queries/entries.sql` — `InsertEntry`, `ListEntriesByTransaction`, `ListEntriesByAccount`, `SumEntriesByAccount` (derived balance).
- [x] `internal/ledger/postgres/queries/balances.sql` — `GetCachedBalance`, `UpsertCachedBalance`.
- [x] `make sqlc` generates `internal/ledger/postgres/ledgerdb`; commit the generated code. (sqlc.yaml: uuid→google/uuid, timestamptz→time.Time overrides.)
- [x] `internal/ledger/postgres/pool.go` — `pgxpool` constructor from `DB_URL`; wire into `cmd/ledger`. (constructor done; cmd/ledger wiring lands in NS-106.)
- [x] `internal/ledger/postgres/repository.go` — repository over `ledgerdb.Queries` + a `WithSerializableTx(ctx, fn)` tx runner.

## NS-102 · `PostTransaction` at SERIALIZABLE (FR-L1/L2)
- [x] Domain types `internal/ledger/{account,entry,transaction}.go` — `EntryInput{AccountCode, Direction, Amount money.Amount}`, `PostRequest{Reference, Type, Entries…}`.
- [x] `internal/ledger/service.go` `PostTransaction`: open a SERIALIZABLE tx → insert transaction → insert entries → commit; application-side balance check (`sum(debits) == sum(credits)`) before commit, with the DEFERRED DB trigger as backstop.
- [x] Typed errors: `ErrUnbalanced`, `ErrTooFewEntries` (<2), `ErrMixedCurrency`, `ErrUnknownAccount`. (Plus `ErrNonPositiveAmount`.)
- [x] Serialization-failure retry wrapper: detect pgx `40001`, retry with bounded attempts + backoff (ADR-0002).
- [x] Unit tests: balanced posts succeed; unbalanced / single-entry / mixed-currency are rejected.

## NS-103 · `GetBalance` derived + optional cache (FR-L4)
- [ ] `GetBalance(accountCode)` derives from entries via `SumEntriesByAccount`, applying the account's normal-balance direction (asset/expense debit-normal; liability/equity/revenue credit-normal).
- [ ] Update `account_balances` in the **same** serializable txn as `PostTransaction` (optional cache).
- [ ] Test: derived balance == cached balance after a random series of posts.

## NS-104 · Append-only enforcement (FR-L3)
- [ ] Audit the repository: entries are insert-only — no UPDATE/DELETE path anywhere.
- [ ] Test asserting the DB trigger `trg_entries_no_update` rejects a raw UPDATE and a DELETE on `entries` (expect error).

## NS-105 · Property-based conservation test (AC-2)
- [ ] Add `pgregory.net/rapid` to `go.mod`.
- [ ] Property: generate random *balanced* transaction sets across N seeded accounts; after posting all, assert the sum of every account balance equals the starting total (value conserved).
- [ ] Property: generated *unbalanced* sets are always rejected by `PostTransaction` (never committed).

## NS-106 · ledger gRPC surface + `ExportTransactions` (FR-L5)
- [ ] Expand `api/proto/ledger/v1/ledger.proto`: `PostTransaction`, `GetBalance`, `GetAccount`, `ListEntries`, `ExportTransactions(window)`; keep `Ping`. `make proto`.
- [ ] `internal/ledger/grpc.go` — gRPC server mapping proto ⇄ domain; register on `:50051` in `cmd/ledger` (replace the empty `serviceboot` surface).
- [ ] `ExportTransactions` streams `canonical.Record`s for a time window (status/type/amounts mapped from the journal).
- [ ] Mapping unit test (proto ⇄ `canonical.Record`) + a gRPC smoke test.

## Verification (Sprint 1 DoD)
1. [ ] `make sqlc` + `make proto` regenerate cleanly; `make build` green.
2. [ ] `go test ./... -race` green, including the `rapid` conservation property (AC-2).
3. [ ] `make seed`, then post a balanced transfer over gRPC; `GetBalance` matches the hand-computed figure.
4. [ ] Drop the `account_balances` cache and re-derive balances from `entries` alone — identical (journal is the source of truth).
5. [ ] `ExportTransactions` over a window returns valid `canonical.Record`s (round-trips through JSON).
6. [ ] `make lint` clean.

---

# Sprint 2 — Switch MVP · Task Tracker

Source of truth for Sprint 2 progress. Same rule: implement → verify → tick → commit → next. No batching.

**Goal:** a real transfer goes in over REST and money moves, once.
**DoD:** an end-to-end happy-path transfer settles; a duplicate `Idempotency-Key` is a no-op; the trace spans switch → rail → ledger.

**Decisions:** public REST via `chi` on `:8080`; switch gRPC `:50052`; `mockrail` on `:50053`; transfer domain in `internal/switch`; ledger reached via gRPC client; idempotency durable in Postgres with a Redis (`REDIS_ADDR`) fast-path (ADR-0003); externalized transfer state lives in `transactions.status`.

## NS-201 · REST `POST /v1/transfers` (FR-T1)
- [ ] `internal/switch/transport/rest.go` — `chi` router; `POST /v1/transfers` decoding `{source, destination, amount_minor, currency, reference}` + a required `Idempotency-Key` header; request validation (positive amount, known currency).
- [ ] `GET /v1/transfers/{id}` — return the current transfer state.
- [ ] `api/openapi/switch.yaml` — document both endpoints + error shapes.
- [ ] Wire the router into `cmd/switchd` alongside `/healthz` (via `serviceboot`).

## NS-202 · Durable idempotency store (FR-T2, ADR-0003)
- [ ] `internal/switch/idempotency.go` — reserve the key (`status=in_progress`, store `request_fingerprint`) in `idempotency_keys`; on completion store `response` + `transaction_id` + `status`.
- [ ] Redis fast-path: check/set the key in Redis; a miss falls through to Postgres (the durable record of truth).
- [ ] Replay of a completed key returns the **stored** result; same key + different fingerprint → `409 Conflict`.
- [ ] Tests: first call processes; replay returns the stored result; concurrent `in_progress` handled.

## NS-203 · Transfer state machine — happy path (FR-T3)
- [ ] `internal/switch/statemachine.go` — encode the ARCHITECTURE §4 transitions; `INITIATED → DEBIT_PENDING → DEBITED → AWAITING_SETTLEMENT → SETTLED`.
- [ ] `internal/switch/orchestrator.go` — drive a transfer through the machine, persisting each state in `transactions.status` (single externalized source of state).
- [ ] Unit tests: legal transitions advance; illegal transitions error.

## NS-204 · `mockrail` v1 — success path (ARCHITECTURE §2.3)
- [ ] `internal/mockrail/server.go` — `SendToRail` returns success after a configurable latency (`MOCKRAIL_LATENCY_MS`); serve on `:50053` in `cmd/mockrail`.
- [ ] `internal/switch/railclient.go` — switch → mockrail client.

## NS-205 · switch → ledger debit/credit (FR-T3)
- [ ] `internal/switch/ledgerclient.go` — gRPC client to ledger `:50051`.
- [ ] On `DEBITED`: post debit(source) → credit(`SETTLEMENT`); on `SETTLED`: post debit(`SETTLEMENT`) → credit(destination). Each call carries the transfer `reference`.
- [ ] Test: after a happy-path settle, ledger balances reflect the move exactly once.

## NS-206 · correlation-id + tracing (NFR-7)
- [ ] Propagate `correlation_id` from the REST request (or generate one) through rail + ledger calls via context + `pkg/logging`.
- [ ] OTel spans link switch → rail → ledger into one trace.
- [ ] Manual: a transfer shows as a single end-to-end trace in Jaeger (`:16686`).

## Verification (Sprint 2 DoD)
1. [ ] Run `ledger`, `mockrail`, `switchd`; `curl POST /v1/transfers` settles; `GET /v1/transfers/{id}` shows `SETTLED`.
2. [ ] Repeat with the same `Idempotency-Key` → identical response, no second transaction (DB shows one).
3. [ ] Same key + altered body → `409`.
4. [ ] Jaeger shows one trace spanning switch → rail → ledger.
5. [ ] `go test ./... -race` + `make lint` green.

---

# Sprint 3 — Reversals + resilience · Task Tracker

Source of truth for Sprint 3 progress. Same rule: implement → verify → tick → commit → next. No batching.

**Goal:** the headline guarantee — no debit is ever left stranded.
**DoD:** AC-1 passes; a mid-flow crash leaves no stranded debit after restart; dashboards show the outcome split.

**Decisions:** transactional outbox in `internal/switch/outbox` (writer in the same txn as the state change; poller scans `idx_outbox_unpublished`, at-least-once → idempotent handlers, ADR-0004); reversals are parent-linked compensating ledger transactions; rail callbacks arrive on switch gRPC `:50052`; chaos is mockrail-side and deterministic by seed.

## NS-301 · Transactional outbox — writer + poller (FR-R3, ADR-0004)
- [ ] `internal/switch/outbox/writer.go` — append an `outbox` row in the same DB txn as the state change (no dual-write).
- [ ] `internal/switch/outbox/poller.go` — poll unpublished rows (`published_at IS NULL`), dispatch to a handler, mark `published_at`; bounded batch + interval.
- [ ] Handlers are idempotent (at-least-once delivery).
- [ ] `outbox_lag` gauge wired via `pkg/metrics`.

## NS-302 · Reversal as compensating transaction (FR-R1, FR-R2)
- [ ] Reversal = a new ledger transaction with `parent_transaction_id` set, posting the inverse entries that restore the source (append-only; never edits the journal).
- [ ] Idempotent: re-running a reversal for an already-reversed parent is a no-op (guard on parent + status).
- [ ] Tests: source restored exactly; double-reversal is a no-op.

## NS-303 · In-doubt handling (FR-T4)
- [ ] On rail timeout/unknown, route `AWAITING_SETTLEMENT → REVERSAL_PENDING` and enqueue a reversal via the outbox (in-doubt → reverse; never assume success/failure).
- [ ] `REVERSAL_PENDING → REVERSED` once the compensating entries post.

## NS-304 · Idempotent duplicate rail callbacks (FR-T5)
- [ ] `internal/switch/grpc.go` — switch gRPC rail-callback intake; a second "success" for an already-terminal transfer is a no-op (terminal-state guard).
- [ ] Test: a duplicate callback changes nothing.

## NS-305 · `mockrail` chaos (ARCHITECTURE §2.3)
- [ ] Env-seeded probabilities: added latency, hard timeout (no response), duplicate-success callback, explicit decline.
- [ ] Deterministic by `MOCKRAIL_SEED` so a run is reproducible.

## NS-306 · Crash recovery (NFR-4)
- [ ] Kill `switchd` mid-flow (between debit and settlement); on restart the poller resumes pending reversals/rail calls from the outbox.
- [ ] Scripted verification that no work is lost.

## NS-307 · Chaos test — zero stranded debits (AC-1)
- [ ] `test/chaos` — drive N transfers with mockrail injecting timeouts/duplicates + a mid-flow kill; assert every debit ends matched by a credit or a completed reversal (zero stranded).

## NS-308 · Metrics (NFR-7)
- [ ] Transfer outcome counters by terminal state (`settled` / `reversed` / `failed`).
- [ ] Reversal-latency histogram.
- [ ] Outbox-lag gauge (from NS-301) surfaced on `/metrics`.

## Verification (Sprint 3 DoD)
1. [ ] `test/chaos` ends with zero stranded debits over N transfers (AC-1).
2. [ ] Kill `switchd` mid-flow; after restart the debit is matched by a completed reversal — no stranded debit.
3. [ ] A duplicate rail callback is a no-op (state unchanged).
4. [ ] Prometheus shows the outcome split + reversal-latency histogram + outbox lag.
5. [ ] `go test ./... -race` + `make lint` green.

---

# Sprint 4 — Reconcile CLI · Task Tracker

Source of truth for Sprint 4 progress. Same rule: implement → verify → tick → commit → next. No batching.

**Goal:** prove, after the fact, that internal and external truth agree — and find every gap.
**DoD:** AC-3 and AC-4 pass; a large generated file reconciles in seconds; reports are reproducible.

**Decisions:** `reconcile` is a `cobra` CLI (`cmd/reconcile`) configured via `viper` (flags/env); adapters normalize every input to `canonical.Record`; the matcher keys on `reference` with exact-amount + timestamp-window tolerance; runs persist to `recon_runs` + `recon_exceptions`; output is order-independent and re-runnable without double-counting.

## NS-401 · Cobra CLI + Viper config (FR-C)
- [ ] Flesh out `cmd/reconcile run`: `--internal`, `--external`, `--tolerance-window`, `--format=text|json` via viper (flags + env).
- [ ] Replace the Sprint-0 "not implemented" stub with the real command wiring.

## NS-402 · Adapters → canonical (FR-C1)
- [ ] `internal/reconcile/adapters/ledger.go` — read the ledger `ExportTransactions` output → `canonical.Record`.
- [ ] `internal/reconcile/adapters/nibss.go` — NIBSS-style settlement reader → canonical.
- [ ] `internal/reconcile/adapters/csv.go` — generic CSV settlement reader → canonical.
- [ ] Adapter unit tests (messy formats map cleanly; nothing downstream sees a raw row).

## NS-403 · Streaming matcher (FR-C2, FR-C7)
- [ ] `internal/reconcile/matcher.go` — working index keyed by `reference`; match on exact `amount_minor` + `initiated_at` within the configurable window.
- [ ] Stream inputs (don't hold whole files in memory); the index is keyed, not the full file.

## NS-404 · Exception categories (FR-C3)
- [ ] `internal/reconcile/exceptions.go` — `unmatched_internal`, `unmatched_external`, `amount_mismatch`, `pending_reversal`, `duplicate`.
- [ ] `pending_reversal` detection: a timed-out/failed internal record whose reversal hasn't settled (this feeds Sprint 5's loop).

## NS-405 · Reports + persistence (FR-C4, FR-C5)
- [ ] `internal/reconcile/report.go` — human-readable text + machine-readable JSON.
- [ ] `internal/reconcile/store.go` — persist `recon_runs` (inputs, timestamps, counts, summary) + `recon_exceptions` rows.

## NS-406 · Determinism (FR-C6, AC-4)
- [ ] Sort/key deterministically so output is independent of input row order and worker scheduling.
- [ ] Re-running the same inputs does not double-count (idempotent persistence).

## NS-407 · `scripts/gen_settlement` — fixture generator
- [ ] Generate a settlement file with K injected discrepancies spanning every category (unmatched, amount-mismatch, duplicate, pending-reversal).
- [ ] Seeded/deterministic so fixtures are reproducible.

## NS-408 · Fixture test — 100% recall (AC-3, AC-4)
- [ ] Fixture pair with K injected discrepancies; assert 100% recall and correct category labels (AC-3).
- [ ] Assert a second run yields identical reports with no double-counted exceptions (AC-4).

## Verification (Sprint 4 DoD)
1. [ ] `make gen-settlement`, then `make reconcile INTERNAL=… EXTERNAL=…` produces a text + JSON report.
2. [ ] Every injected discrepancy is found and correctly categorized (AC-3).
3. [ ] Re-running the same inputs gives identical reports and no new `recon_exceptions` (AC-4).
4. [ ] A large generated file reconciles in seconds (streaming, not full-load).
5. [ ] `go test ./... -race` + `make lint` green.

---

# Sprint 5 — Feedback + hardening · Task Tracker

Source of truth for Sprint 5 progress. Same rule: implement → verify → tick → commit → next. No batching.

**Goal:** close the loop and prove the system holds under load.
**DoD:** AC-5 and AC-6 pass; integration suite green in CI; load numbers recorded with a dashboard screenshot.

**Decisions:** reconcile emits `pending_reversal` exceptions to switchd (API/queue); switchd exposes a corrective endpoint that triggers a re-reversal through the existing outbox path; integration tests use `testcontainers-go` (`-tags=integration`, `make test-integration`); load via `k6` against NFR-2/3.

## NS-501 · Reconcile → switchd feedback (FR-F1)
- [ ] On a `pending_reversal` exception, emit it to switchd (corrective API call or queue) carrying the offending reference/transaction.

## NS-502 · switchd corrective endpoint → re-reversal (AC-5)
- [ ] switchd endpoint consumes the feedback and triggers a re-reversal through the existing outbox/reversal path.
- [ ] The next reconcile run shows the exception resolved (AC-5).

## NS-503 · testcontainers integration suite (NFR-9)
- [ ] `test/integration` (`-tags=integration`): real-Postgres serializable posting, idempotent replays, reversal recovery after a simulated restart.
- [ ] Wire `make test-integration` into CI.

## NS-504 · k6 load test (AC-6, NFR-2/3)
- [ ] `test/load/transfers.js` — drive `POST /v1/transfers`; tune toward ≥500 tps / p99 < 250 ms (excluding the injected rail delay).
- [ ] Capture achieved throughput + p99 and a dashboard screenshot.

## NS-505 · Backpressure / serialization-retry on the ledger path (ADR-0002)
- [ ] Bounded retry + backpressure on `40001` serialization failures so the SERIALIZABLE ledger path degrades gracefully under load.

## NS-506 · Error taxonomy + structured REST errors
- [ ] Define an error taxonomy; map domain errors to structured REST error responses (stable codes, JSON body) on the public API.

## Verification (Sprint 5 DoD)
1. [ ] Inject a stuck `pending_reversal`; reconcile feeds switchd; the re-reversal fires; the next run shows it resolved (AC-5).
2. [ ] `make test-integration` green locally and in CI.
3. [ ] A k6 run records throughput + p99 against NFR-2/3 with a dashboard screenshot (AC-6).
4. [ ] The load run shows graceful behavior under serialization retries (no stranded debits, bounded latency).
5. [ ] REST errors return structured, documented shapes.

---

# Sprint 6 — Polish + portfolio · Task Tracker

Source of truth for Sprint 6 progress. Same rule: implement → verify → tick → commit → next. No batching.

**Goal:** make it legible to a reader and turn it into portfolio signal.
**DoD:** a newcomer can clone, `make dev`, and run the demo from the README in under 15 minutes.

**Decisions:** dashboards + deploy assets under `deployments/`; ADRs completed in `docs/adr`; the optional breadth track (NS-606) stays a *separate* repo — do not bloat this one.

## NS-601 · Finalize README
- [ ] README with the architecture diagram (`docs/diagrams/data-flow.svg`) and a "failure modes" section; a quickstart that matches the Makefile.

## NS-602 · Grafana dashboards
- [ ] Commit Grafana dashboards + provisioning under `deployments/` (alongside `prometheus.yml`).

## NS-603 · Scripted demo
- [ ] One script: fire transfers under chaos → show zero stranded debits → run reconcile → trigger a re-reversal → show it resolved.

## NS-604 · Complete the ADRs
- [ ] Fill `docs/adr/0001…0005` from stubs to full context → decision → consequences.

## NS-605 · Build-log posts
- [ ] Write the ROADMAP portfolio-checkpoint posts (after Sprints 1/3/4/5), each leading with the Nigerian number + the engineering decision + the rejected alternative.

## NS-606 · (Optional, deferred) breadth track
- [ ] If pursued, spin up a **separate** repo (USSD engine or offline-sync) rather than expanding this one.

## Verification (Sprint 6 DoD)
1. [ ] Fresh clone → `make dev` → `make migrate-up` → `make seed` → run the demo from the README in under 15 minutes.
2. [ ] Grafana shows the chaos-run dashboards from committed assets.
3. [ ] All five ADRs are complete (no stub sections).
4. [ ] `go test ./... -race`, `make test-integration`, and `make lint` all green.
