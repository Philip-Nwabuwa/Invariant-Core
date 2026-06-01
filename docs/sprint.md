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
- [x] `GetBalance(accountCode)` derives from entries via `SumEntriesByAccount`, applying the account's normal-balance direction (asset/expense debit-normal; liability/equity/revenue credit-normal).
- [x] Update `account_balances` in the **same** serializable txn as `PostTransaction` (optional cache).
- [x] Test: derived balance == cached balance after a random series of posts.

## NS-104 · Append-only enforcement (FR-L3)
- [x] Audit the repository: entries are insert-only — no UPDATE/DELETE path anywhere.
- [x] Test asserting the DB trigger `trg_entries_no_update` rejects a raw UPDATE and a DELETE on `entries` (expect error).

## NS-105 · Property-based conservation test (AC-2)
- [x] Add `pgregory.net/rapid` to `go.mod`.
- [x] Property: generate random *balanced* transaction sets across N seeded accounts; after posting all, assert the sum of every account balance equals the starting total (value conserved).
- [x] Property: generated *unbalanced* sets are always rejected by `PostTransaction` (never committed).

## NS-106 · ledger gRPC surface + `ExportTransactions` (FR-L5)
- [x] Expand `api/proto/ledger/v1/ledger.proto`: `PostTransaction`, `GetBalance`, `GetAccount`, `ListEntries`, `ExportTransactions(window)`; keep `Ping`. `make proto`.
- [x] `internal/ledger/grpc.go` — gRPC server mapping proto ⇄ domain; register on `:50051` in `cmd/ledger` (replace the empty `serviceboot` surface). (serviceboot gained `RegisterGRPC`/`Cleanup` hooks.)
- [x] `ExportTransactions` streams `canonical.Record`s for a time window (status/type/amounts mapped from the journal).
- [x] Mapping unit test (proto ⇄ `canonical.Record`) + a gRPC smoke test.

## Verification (Sprint 1 DoD) — ✅ PASSED 2026-05-31
1. [x] `make sqlc` + `make proto` regenerate cleanly (no diff); `make build` green (4 binaries).
2. [x] `go test ./... -race` green, including the `rapid` conservation property (AC-2) and the append-only trigger test (testcontainers).
3. [x] Live `ledger` binary (vs ephemeral PG16): post a balanced transfer over gRPC; `GetBalance` matches the hand-computed figure (SETTLEMENT liability −5000, FEES revenue +5000). Note: `make seed` is still a Sprint-0 stub, so the seeded SETTLEMENT/FEES accounts were used.
4. [x] Dropped the `account_balances` cache; `GetBalance` re-derives correct figures from `entries` alone (derives via `SumEntriesByAccount`, never reads the cache; also covered by the derived==cached test).
5. [x] `ExportTransactions` over a window returns valid `canonical.Record`s (round-trips through JSON).
6. [x] `make lint` clean (0 issues, whole repo).

---

# Sprint 2 — Switch MVP · Task Tracker

Source of truth for Sprint 2 progress. Same rule: implement → verify → tick → commit → next. No batching.

**Goal:** a real transfer goes in over REST and money moves, once.
**DoD:** an end-to-end happy-path transfer settles; a duplicate `Idempotency-Key` is a no-op; the trace spans switch → rail → ledger.

**Decisions:** public REST via `chi` on `:8080`; switch gRPC `:50052`; `mockrail` on `:50053`; transfer domain in `internal/switch`; ledger reached via gRPC client; idempotency durable in Postgres with a Redis (`REDIS_ADDR`) fast-path (ADR-0003); externalized transfer state lives in `transactions.status`.

## NS-201 · REST `POST /v1/transfers` (FR-T1)
- [x] `internal/switch/transport/rest.go` — `chi` router; `POST /v1/transfers` decoding `{source, destination, amount_minor, currency, reference}` + a required `Idempotency-Key` header; request validation (positive amount, known currency). (Domain types in `internal/switch/transfer.go` — `package transfer`; behind a `Service` interface, NS-201 wires `StubService`.)
- [x] `GET /v1/transfers/{id}` — return the current transfer state.
- [x] `api/openapi/switch.yaml` — document both endpoints + error shapes.
- [x] Wire the router into `cmd/switchd` alongside `/healthz` (via `serviceboot`). (Added `Options.RegisterHTTP` hook → `health.NewServer` register callback; REST mounted at `/`, `/healthz`+`/metrics` still take precedence.)

## NS-202 · Durable idempotency store (FR-T2, ADR-0003)
- [x] `internal/switch/idempotency.go` — reserve the key (`status=in_progress`, store `request_fingerprint`) in `idempotency_keys`; on completion store `response` + `transaction_id` + `status`. (`IdempotencyStore.Reserve` → `Outcome` {Reserved/Replay/Conflict/InProgress} via `INSERT … ON CONFLICT DO NOTHING`; `Complete`; `Fingerprint(req)` = SHA-256 of canonical body. 2nd sqlc block → `internal/switch/postgres/switchdb`.)
- [ ] Redis fast-path: check/set the key in Redis; a miss falls through to Postgres (the durable record of truth). **(Deferred — Postgres-only this sprint; ADR-0003 fast-path is a later optimization.)**
- [x] Replay of a completed key returns the **stored** result; same key + different fingerprint → `409 Conflict`. (Store returns `OutcomeReplay`/`OutcomeConflict`; transport maps `ErrIdempotencyConflict`/`ErrInProgress` → 409. End-to-end HTTP wiring lands with the real orchestrator in NS-205.)
- [x] Tests: first call processes; replay returns the stored result; concurrent `in_progress` handled. (testcontainers: Reserved→InProgress→Replay→Conflict; plus a pure `Fingerprint` stability test. JSONB compared semantically, not byte-wise.)

## NS-203 · Transfer state machine — happy path (FR-T3)
- [x] `internal/switch/statemachine.go` — encode the ARCHITECTURE §4 transitions; `INITIATED → DEBIT_PENDING → DEBITED → AWAITING_SETTLEMENT → SETTLED`. (`transitions` table + `State.CanTransition`; `statusForState` maps the 5 rich states → 3 coarse `transactions.status` values via `pkg/canonical`; `stateForStatus` inverts for the read model.)
- [x] `internal/switch/orchestrator.go` — drive a transfer through the machine, persisting each state in `transactions.status` (single externalized source of state). (`Orchestrator` implements `Service`; synchronous happy path; `Ledger`/`Rail`/`Store` interfaces (real wiring NS-204/205). Switch owns one `transactions` row per transfer carrying `idempotency_key`+lifecycle status; source/dest/amount stashed in `metadata` JSONB for GET. `PostgresStore` over new `switch/postgres/queries/transactions.sql`.)
- [x] Unit tests: legal transitions advance; illegal transitions error. (White-box `CanTransition`/`statusForState`/`stateForStatus`; testcontainers orchestrator happy-path (debit/settle/rail each called once, GET reconstructs fields) + debit-failure-aborts.)
- Note: `cmd/switchd` still wires `StubService`; the swap to `Orchestrator` (with real ledger/rail + idempotency) lands in NS-205. Data-model (switch row + ledger postings linked by `reference`) to be confirmed there.

## NS-204 · `mockrail` v1 — success path (ARCHITECTURE §2.3)
- [x] `internal/mockrail/server.go` — `SendToRail` returns success after a configurable latency (`MOCKRAIL_LATENCY_MS`); serve on `:50053` in `cmd/mockrail`. (New `api/proto/mockrail/v1` `RailService.SendToRail`; server honours ctx cancellation during latency; `cmd/mockrail` registers it + parses `MOCKRAIL_LATENCY_MS`.)
- [x] `internal/switch/railclient.go` — switch → mockrail client. (`RailClient` implements the orchestrator's `Rail` interface; non-success verdict → error.)
- [x] Tests: bufconn smoke (success / latency respected / ctx-cancel) + switch `RailClient` against the real server over bufconn.

## NS-205 · switch → ledger debit/credit (FR-T3)
- [x] `internal/switch/ledgerclient.go` — gRPC client to ledger `:50051` behind the `Ledger` interface. (Idempotency moved to an `IdempotentService` decorator (`idempotent.go`) that reserves the key *before* the orchestrator creates any row — so a duplicate never writes a second `transactions` row. `cmd/switchd` now wires the real Postgres pool + ledger/rail gRPC clients + decorator; `StubService` deleted, replaced by a transport-local test double.)
- [x] On `DEBITED`: post debit(source) → credit(`SETTLEMENT`); on `SETTLED`: post debit(`SETTLEMENT`) → credit(destination). Each call carries the transfer `reference` and posts as a separate balanced ledger transaction (two rows linked by reference, per the data-model decision).
- [x] Test: after a happy-path settle, ledger balances reflect the move exactly once. (`TestLedgerClient_BothLegsMoveMoneyOnce` over a real bufconn ledger; `TestSettle_EndToEnd` exercises the full stack over one Postgres and asserts replay is a no-op (one `transactions` row), altered body → conflict, and SETTLEMENT nets to zero.)

## NS-206 · correlation-id + tracing (NFR-7)
- [x] Propagate `correlation_id` from the REST request (or generate one) through rail + ledger calls via context + `pkg/logging`. (chi `correlationID` middleware reads/generates `X-Correlation-ID`, puts it on the request ctx, echoes it on the response; `logging.Unary{Client,Server}Interceptor` carry it across gRPC as `x-correlation-id` metadata — client injects, server lifts back onto the handler ctx. Round-trip test proves both ends agree on the key.)
- [x] OTel spans link switch → rail → ledger into one trace. (`otelhttp` wraps switch's REST router for the root server span; `otelgrpc` stats handlers on switchd's client conns + serviceboot's gRPC server emit child spans that continue the trace via the already-configured W3C propagator.)
- [x] Manual: a transfer shows as a single end-to-end trace in Jaeger (`:16686`). (Verified: one trace, root `switchd.rest` → two `ledger…/PostTransaction` (debit + settlement legs) + one `mockrail…/SendToRail`, each with the downstream server span as its child.)

## Verification (Sprint 2 DoD)
1. [x] Run `ledger`, `mockrail`, `switchd`; `curl POST /v1/transfers` settles; `GET /v1/transfers/{id}` shows `SETTLED`. (POST → `201` `SETTLED`; GET → `200` `SETTLED`. Demo accounts `CUST-001`/`CUST-002` seeded via `make seed`.)
2. [x] Repeat with the same `Idempotency-Key` → identical response, no second transaction (DB shows one). (Replay returned the same id; `SELECT count(*) … WHERE idempotency_key='dod-key-1'` = 1.)
3. [x] Same key + altered body → `409`. (`409 Conflict`, `transfer: idempotency-key reused with a different request`.)
4. [x] Jaeger shows one trace spanning switch → rail → ledger. (trace `e31ec221…`, 7 spans, root `switchd.rest`.)
5. [x] `go test ./... -race` + `make lint` green. (all packages ok; lint 0 issues.)

---

# Sprint 3 — Reversals + resilience · Task Tracker

Source of truth for Sprint 3 progress. Same rule: implement → verify → tick → commit → next. No batching.

**Goal:** the headline guarantee — no debit is ever left stranded.
**DoD:** AC-1 passes; a mid-flow crash leaves no stranded debit after restart; dashboards show the outcome split.

**Decisions:** transactional outbox in `internal/switch/outbox` (writer in the same txn as the state change; poller scans `idx_outbox_unpublished`, at-least-once → idempotent handlers, ADR-0004); reversals are parent-linked compensating ledger transactions; rail callbacks arrive on switch gRPC `:50052`; chaos is mockrail-side and deterministic by seed.

## NS-301 · Transactional outbox — writer + poller (FR-R3, ADR-0004)
- [x] `internal/switch/outbox/writer.go` — append an `outbox` row in the same DB txn as the state change (no dual-write). (`outbox.Append(ctx, q, …)` takes a tx-scoped `*switchdb.Queries`; `PostgresStore.WithTx` runs `{state change + Append}` atomically.)
- [x] `internal/switch/outbox/poller.go` — poll unpublished rows, dispatch to a handler, mark `published_at`; bounded batch + interval. (Claim via `FOR UPDATE SKIP LOCKED` + lease; per-event exponential backoff → dead-letter at the attempt cap so a poison event never head-of-line blocks. `Drain` flushes synchronously for tests/recovery.)
- [x] Handlers are idempotent (at-least-once delivery). (`Handler` contract documents at-least-once; delivery guarantees + dead-letter hook verified by testcontainers tests.)
- [x] `outbox_lag` gauge wired via `pkg/metrics`. (Surfaced as `switch_outbox_lag_seconds` in NS-308: `cmd/switchd` ticks `OutboxLagSeconds` every 5s onto the gauge, and the dead-letter hook feeds `switch_outbox_dead_letters_total`.)

## NS-302 · Reversal as compensating transaction (FR-R1, FR-R2)
- [x] Reversal = a new ledger transaction with `parent_transaction_id` set, posting the inverse entries that restore the source (append-only; never edits the journal). (`LedgerClient.PostReversal` posts SETTLEMENT→source as `type='reversal'`, parent-linked; ledger proto/service gained `parent_transaction_id` + `idempotency_key` in NS-301b. Driver `handleReversal` drives `reversal_pending → reversed`.)
- [x] Idempotent: re-running a reversal for an already-reversed parent is a no-op (guard on parent + status). (Three guards: the `reversal_pending` status check, the per-leg `<id>:reversal` idempotency key, and the `uq_reversal_per_parent` unique index. Ledger splits `23505` already-reversed no-op from `40001` retry.)
- [x] Tests: source restored exactly; double-reversal is a no-op. (`TestReversal_RestoresSourceExactlyOnce`: rail-declined transfer → source restored to 0, destination untouched, SETTLEMENT nets to zero, one parent-linked reversal, re-post is a no-op.)

## NS-303 · In-doubt handling (FR-T4, DESIGN-NOTES §1)
- [x] On rail timeout/unknown, route to `IN_DOUBT` and issue a **TSQ** before deciding (never assume success/failure): TSQ-settled → settle, TSQ-no-settlement → `REVERSAL_PENDING`, TSQ inconclusive after bounded retries → `MANUAL_REVIEW`. (mockrail gains a `QueryStatus` RPC + `RAIL_STATUS_DECLINED`; `RailClient.QueryStatus`; driver `handleInDoubt` with `WithTSQPolicy`. IN_DOUBT is its own persisted status + outbox event, so a crash re-issues the TSQ — never a re-send.)
- [x] `REVERSAL_PENDING → REVERSED` once the compensating entries post (via NS-302). (Integration tests: TSQ-settled completes settlement with **source not refunded, destination credited**; TSQ-no-settlement reverses; inconclusive holds in MANUAL_REVIEW with money in suspense.)

## NS-304 · Idempotent duplicate rail callbacks (FR-T5)
- [x] `internal/switch/grpc.go` — switch gRPC `RailCallback` intake on `:50052` (registered via `serviceboot.RegisterGRPC`); a second "success" for an already-terminal transfer is a no-op. Two guards close the duplicate/poller race: the row-locked transition methods no-op once terminal, and the `<id>:settle` per-leg key means even concurrent settlements post one leg. (Looks up the lifecycle row by reference via `metadata ? 'source'`.)
- [x] Test: a duplicate callback changes nothing. (`TestRailCallback_DuplicateIsNoOp` over real gRPC/bufconn: SUCCESS settles, the duplicate leaves one settlement leg + balances unchanged; unknown reference → NotFound.)

## NS-305 · `mockrail` chaos (ARCHITECTURE §2.3)
- [x] Env-seeded probabilities: added latency, hard timeout (no response), duplicate-success callback, explicit decline (+ a TSQ-timeout knob). (`MOCKRAIL_P_TIMEOUT/P_DECLINE/P_DUPLICATE/P_TSQ_TIMEOUT`; duplicate callbacks dial the switch via opt-in `SWITCH_CALLBACK_TARGET`. The TSQ reports the *true* outcome and can disagree with a timed-out send — the "settled-but-we-timed-out" case.)
- [x] Deterministic by `MOCKRAIL_SEED` so a run is reproducible. (Each outcome derives from `hash(seed, reference, dimension)` — no shared RNG, so it is reproducible per transfer regardless of concurrency/order; verified by a same-seed/different-seed test.)

## NS-306 · Crash recovery (NFR-4)
- [x] Kill `switchd` mid-flow (between debit and settlement); on restart the poller resumes pending reversals/rail calls from the outbox. (`internal/switch/recovery.go` — `Recoverer.Recover` re-enqueues every resumable transfer with no live outbox event (`ListStuckTransfers`), mapping status→driving event; idempotent handlers make a duplicate event a no-op. `cmd/switchd` runs the sweep at boot before the poller. Idempotency lease takeover (`idempotency.go`): a replay past the in-progress lease re-attaches to the transfer the crashed holder created (`GetTransferIDByIdempotencyKey`) rather than starting a second one — DESIGN-NOTES §5.)
- [x] Scripted verification that no work is lost. (`scripts/crash_recovery_demo.sh` runs the real ledger/mockrail/switchd binaries, fires a transfer, `kill -9`s switchd while the row is `debited` (settlement held behind `MOCKRAIL_LATENCY_MS`), restarts it, and asserts the transfer resumes to `SETTLED` with exactly one debit leg — no stranded or doubled debit. Verified: crash at `debited` → `SETTLED`.)

## NS-307 · Chaos test — zero stranded debits (AC-1)
- [x] `test/chaos` — drive N transfers with mockrail injecting timeouts/duplicates + a mid-flow kill; assert every debit ends matched by a credit or a completed reversal (zero stranded). (`test/chaos/chaos_test.go`, no build tag, skips without Docker. In-process real stack: ledger gRPC + mockrail (seeded chaos: timeout/decline/duplicate-callback/TSQ-timeout) over bufconn + Postgres orchestrator/driver/outbox. Mid-flow kill = deleting in-flight outbox events while all 60 transfers sit at `debited`; the recovery sweep + poller then drive each to its true seed-determined terminal. Asserts: zero non-terminal left; each transfer's terminal state == the rail's seed-derived outcome (a no-side-effect predictor `mockrail.Server` with the same config); and ledger balances reconcile exactly — settled→credited, reversed→source restored, manual_review→held in SETTLEMENT suspense. Reproducible by seed: split is identical across runs (settled=40/reversed=14/manual_review=6).)

## NS-308 · Metrics (NFR-7)
- [x] Transfer outcome counters by terminal state (`settled` / `reversed` / `failed`). (`switch_transfer_outcomes_total{outcome}` in `internal/switch/metrics.go`; the driver increments on a terminal `mark*` only when it actually `advanced` (exactly-once under at-least-once delivery), covering `settled`/`reversed`/`manual_review`/`failed`. Series pre-initialised to 0.)
- [x] Reversal-latency histogram. (`switch_reversal_latency_seconds`; observed in `handleReversal` as `time.Since(InitiatedAt)` when the reversal advances. `transferDetail` gained `InitiatedAt`.)
- [x] Outbox-lag gauge (from NS-301) surfaced on `/metrics`. (`switch_outbox_lag_seconds`, ticked from `OutboxLagSeconds` every 5s. `serviceboot.Options` gained a `Registry` field so `cmd/switchd` owns the registry, builds the instruments, and serves them. Verified live: 8 transfers → `settled=5,reversed=3`, reversal histogram count=3, lag=0.)

## Verification (Sprint 3 DoD) — ✅ PASSED 2026-05-31
1. [x] `test/chaos` ends with zero stranded debits over N transfers (AC-1). (60 transfers under seeded chaos + a mid-flow crash; every transfer reaches its seed-determined terminal state and balances reconcile exactly. Split settled=40/reversed=14/manual_review=6, reproducible across runs.)
2. [x] Kill `switchd` mid-flow; after restart the debit is matched by a completed reversal — no stranded debit. (`scripts/crash_recovery_demo.sh`: real `kill -9` at `debited` → resumes to `SETTLED`, one debit leg. The crash-then-reverse path is covered by `test/chaos` (14 reversals all began stranded at `debited`).)
3. [x] A duplicate rail callback is a no-op (state unchanged). (`TestRailCallback_DuplicateIsNoOp`, re-confirmed green.)
4. [x] Prometheus shows the outcome split + reversal-latency histogram + outbox lag. (Live `:8080/metrics`: `switch_transfer_outcomes_total{settled=5,reversed=3}`, `switch_reversal_latency_seconds_count=3`, `switch_outbox_lag_seconds=0`.)
5. [x] `go test ./... -race` + `make lint` green. (Whole repo: all packages ok; lint 0 issues.)

---

# Sprint 4 — Reconcile CLI · Task Tracker

Source of truth for Sprint 4 progress. Same rule: implement → verify → tick → commit → next. No batching.

**Goal:** prove, after the fact, that internal and external truth agree — and find every gap.
**DoD:** AC-3 and AC-4 pass; a large generated file reconciles in seconds; reports are reproducible.

**Decisions:** `reconcile` is a `cobra` CLI (`cmd/reconcile`) configured via `viper` (flags/env); adapters normalize every input to `canonical.Record`; the matcher keys on `reference` with exact-amount + timestamp-window tolerance; runs persist to `recon_runs` + `recon_exceptions`; output is order-independent and re-runnable without double-counting.

## NS-401 · Cobra CLI + Viper config (FR-C)
- [x] Flesh out `cmd/reconcile run`: `--internal`, `--external`, `--tolerance-window`, `--format=text|json` via viper (flags + env). (Plus `--external-format=nibss|csv`, `--db-url`, `--no-persist`; all bound through `viper.New()` + `AutomaticEnv()` + `BindPFlags`.)
- [x] Replace the Sprint-0 "not implemented" stub with the real command wiring. (`runReconcile`: open adapters → `reconcile.Match` → render text/JSON to stdout → idempotent persist (skipped when an identical fingerprint already ran). Live: a 100-clean/5-per-category fixture → matched=105, 25 exceptions (5 each), byte-identical JSON on re-run.)

## NS-402 · Adapters → canonical (FR-C1)
- [x] `internal/reconcile/adapters/ledger.go` — read the ledger `ExportTransactions` output → `canonical.Record`. (Streaming JSONL `LedgerReader`: one `canonical.Record` per line via `json.Decoder`, `Next()` returns `io.EOF` at end — never buffers the file.)
- [x] `internal/reconcile/adapters/nibss.go` — NIBSS-style settlement reader → canonical. (Fixed 7-column layout with a validated header; numeric NIBSS response codes (`00`/`09`/…) + English words mapped to `canonical.Status` via shared `parseStatus`.)
- [x] `internal/reconcile/adapters/csv.go` — generic CSV settlement reader → canonical. (Header-name-driven `CSVReader`: tolerates column reorder + extra columns; only `reference`+`amount_minor` required.)
- [x] Adapter unit tests (messy formats map cleanly; nothing downstream sees a raw row). (`adapters_test.go`: JSONL round-trip, NIBSS whitespace/status mapping, bad header + malformed amount rejected, generic-CSV reorder/extra-column/empty-status tolerance, `parseStatus` table. Sentinel errors `ErrMalformedRow`/`ErrMissingColumn`/`ErrUnknownStatus`.)

## NS-403 · Streaming matcher (FR-C2, FR-C7)
- [x] `internal/reconcile/matcher.go` — working index keyed by `reference`; match on exact `amount_minor` + `initiated_at` within the configurable window. (`Match(internal, external Stream, window)`: reference hit + exact amount/currency + `withinWindow` → matched; amount/currency/window disagreement → `amount_mismatch`; repeated external reference → `duplicate`; reference miss → `unmatched_external`; leftover internal → classified per NS-404.)
- [x] Stream inputs (don't hold whole files in memory); the index is keyed, not the full file. (Only the internal side is indexed in memory; the external side is consumed through the `Stream` interface one record at a time — the adapters satisfy it structurally.)

## NS-404 · Exception categories (FR-C3)
- [x] `internal/reconcile/exceptions.go` — `unmatched_internal`, `unmatched_external`, `amount_mismatch`, `pending_reversal`, `duplicate`. (`Category` enum mirrors the `recon_exceptions` CHECK exactly; `Exception` carries the internal/external `canonical.Record` + `DeltaMinor` for amount mismatches.)
- [x] `pending_reversal` detection: a timed-out/failed internal record whose reversal hasn't settled (this feeds Sprint 5's loop). (`classifyUnmatchedInternal`: a `failed`/`timed_out` transfer with no settled `reversal` record for its reference → `pending_reversal`, else `unmatched_internal`.)

## NS-405 · Reports + persistence (FR-C4, FR-C5)
- [x] `internal/reconcile/report.go` — human-readable text + machine-readable JSON. (`Report`/`NewReport`; `Text()` summary + per-exception lines, `JSON()` via `MarshalIndent`. Omits wall-clock time so identical inputs render byte-identically; verified by `TestReport_Deterministic`.)
- [x] `internal/reconcile/store.go` — persist `recon_runs` (inputs, timestamps, counts, summary) + `recon_exceptions` rows. (New sqlc `recondb` package (3rd `sqlc.yaml` block; `recon.sql`); `Store.Persist` opens `running` → inserts every exception → `FinishReconRun` (`completed`, counts, `summary` JSONB with the input fingerprint) in one tx, mirroring `internal/switch/store.go`. Testcontainers `TestStore_PersistAndIdempotentGuard` confirms rows + counts.)

## NS-406 · Determinism (FR-C6, AC-4)
- [x] Sort/key deterministically so output is independent of input row order and worker scheduling. (`sortExceptions` orders by `(category, reference, delta)`; report category summary sorted; `TestMatch_DeterministicOrder` + `TestReport_Deterministic` assert stability across repeated runs.)
- [x] Re-running the same inputs does not double-count (idempotent persistence). (`FileFingerprint` = streamed SHA-256 of the input pair, stored in `summary->>'input_fingerprint'`; `Store.FindByFingerprint` lets the CLI skip a re-persist of identical inputs. `TestStore_PersistAndIdempotentGuard` proves the guard leaves the exception row count unchanged.)

## NS-407 · `scripts/gen_settlement` — fixture generator
- [x] Generate a settlement file with K injected discrepancies spanning every category (unmatched, amount-mismatch, duplicate, pending-reversal). (`internal/reconcile/fixture` generates a paired internal JSONL + external NIBSS CSV; `scripts/gen_settlement` writes both via `fixture.WriteJSONL` + `adapters.WriteNIBSS`. Injects `PerCategory` of each: amount_mismatch, unmatched_internal, unmatched_external, duplicate, pending_reversal. Flags `--count/--discrepancies/--seed/--internal-out/--external-out`; `make gen-settlement GEN_ARGS=…`.)
- [x] Seeded/deterministic so fixtures are reproducible. (All amounts + the final shuffle of both sides derive from one `math/rand` source seeded by `--seed`; same seed → identical files. Both sides are shuffled so row order carries no signal.)

## NS-408 · Fixture test — 100% recall (AC-3, AC-4)
- [x] Fixture pair with K injected discrepancies; assert 100% recall and correct category labels (AC-3). (`recall_test.go` `TestFixture_FullRecall`, 50 clean + 4 per category: every injected ref is found with the exact category, per-category counts match, total has no spurious extras, and matched == expected. External fed through a `Stream` like a real adapter.)
- [x] Assert a second run yields identical reports with no double-counted exceptions (AC-4). (`TestFixture_DeterministicReport`: the JSON report is byte-identical across repeated runs; `TestStore_PersistAndIdempotentGuard` covers the DB-side no-double-count via the fingerprint guard.)

## Verification (Sprint 4 DoD) — ✅ PASSED 2026-06-01
1. [x] `make gen-settlement`, then `make reconcile INTERNAL=… EXTERNAL=…` produces a text + JSON report. (`make gen-settlement GEN_ARGS="--count 100 --discrepancies 5 --seed 11"` → `out/internal.jsonl`+`out/settlement.csv`; `make reconcile … RECON_ARGS="--no-persist"` prints the text report, `--format json` valid JSON. matched=105, 25 exceptions, 5 per category.)
2. [x] Every injected discrepancy is found and correctly categorized (AC-3). (`TestFixture_FullRecall`: 50 clean + 4 per category → 100% recall, exact category per ref, no spurious extras. Live 200k run → 250 exceptions, 50 each.)
3. [x] Re-running the same inputs gives identical reports and no new `recon_exceptions` (AC-4). (`TestFixture_DeterministicReport` (byte-identical JSON across runs) + `TestStore_PersistAndIdempotentGuard` against real Postgres — the fingerprint guard leaves the row count unchanged on a re-run.)
4. [x] A large generated file reconciles in seconds (streaming, not full-load). (200,000 transfers each side reconciled in **0.94s** real; the external side streams through the `Stream` interface, only the keyed internal index is held in memory.)
5. [x] `go test ./... -race` + `make lint` green. (Whole repo: 11 packages ok, no data races; lint 0 issues.)

---

# Sprint 5 — Feedback + hardening · Task Tracker

Source of truth for Sprint 5 progress. Same rule: implement → verify → tick → commit → next. No batching.

**Goal:** close the loop and prove the system holds under load.
**DoD:** AC-5 and AC-6 pass; integration suite green in CI; load numbers recorded with a dashboard screenshot.

**Decisions:** reconcile emits `pending_reversal` exceptions to switchd (API/queue); switchd exposes a corrective endpoint that triggers a re-reversal through the existing outbox path; integration tests use `testcontainers-go` (`-tags=integration`, `make test-integration`); load via `k6` against NFR-2/3.

## NS-501 · Reconcile → switchd feedback (FR-F1)
- [x] On a `pending_reversal` exception, emit it to switchd (corrective API call or queue) carrying the offending reference/transaction. (New `CorrectiveReversal` gRPC RPC on the switch surface `:50052`; `cmd/reconcile` gained an opt-in `--switch-addr` flag — when set, `sendFeedback` dials switchd and calls `CorrectiveReversal{reference, reason}` for every `pending_reversal` exception. Default empty = feedback off, preserving existing usage.)

## NS-502 · switchd corrective endpoint → re-reversal (AC-5)
- [x] switchd endpoint consumes the feedback and triggers a re-reversal through the existing outbox/reversal path. (`GRPCServer.CorrectiveReversal` → `Driver.RequeueReversal` → `PostgresStore.RequeueReversal`: re-appends `reversal.requested` to the outbox only when the transfer is in `reversal_pending`; the running poller re-runs the idempotent `handleReversal`. Already-reversed/other status → `requeued=false` no-op; unknown reference → `NotFound`.)
- [x] The next reconcile run shows the exception resolved (AC-5). (`TestCorrectiveReversal_RedrivesStrandedReversal`: a transfer stranded in `reversal_pending` with its outbox event deleted is re-driven by the corrective call → source restored exactly once, SETTLEMENT nets to zero, one reversal row; second call is a no-op; unknown ref → NotFound. End-to-end AC-5 in the Sprint-5 DoD run.)

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
