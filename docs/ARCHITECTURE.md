# Architecture — Invariant Core + Reconcile

## 1. System overview

Four small services and one shared contract. The system is split along a single conceptual seam: **prevention** (keep money consistent in real time) versus **detection** (prove it stayed consistent after settlement).

See [diagrams/data-flow.svg](diagrams/data-flow.svg) for the canonical picture. In short:

```
                 ┌─────────────┐
   transfer ───▶ │  switchd    │ ◀── injects timeouts/failures ──▶ ┌──────────┐
   request       │ (engine)    │                                   │ mockrail │
                 └─────┬───────┘                                   └──────────┘
                       │ posts balanced entries
                       ▼
                 ┌─────────────┐        ┌──────────────────┐
                 │  ledger     │        │ settlement file  │ (NIBSS-style)
                 │ (2-entry)   │        └────────┬─────────┘
                 └─────┬───────┘                 │ adapter normalizes
                       │ export                  ▼
                       ▼                  ┌──────────────────┐
                  canonical record ◀──────┤  (same shape)    │
                       │                  └──────────────────┘
                       ▼
                 ┌─────────────┐
                 │  reconcile  │ ──▶ exceptions report
                 └─────┬───────┘
                       │ pending_reversal feedback
                       └────────────────────────────▶ back to switchd
```

## 2. Components

### 2.1 ledger
The source of internal truth. Owns `accounts` and an append-only `entries` journal grouped under `transactions`. Its single hard invariant: for any transaction, `sum(debits) == sum(credits)`.

gRPC surface (`api/proto/ledger/v1`):
- `PostTransaction(reference, type, []Entry)` — atomic, serializable; rejects unbalanced sets.
- `GetBalance(account)` / `GetAccount(account)`.
- `ListEntries(filter)` / `ExportTransactions(window)` — emits canonical records for reconciliation.

### 2.2 switchd
The transfer engine and the heart of the prevention guarantee. Owns the transfer state machine, idempotency, the conversation with the rail, and the reversal/outbox machinery. It does **not** own balances — it asks the ledger to move them.

Public REST surface (`api/openapi/switch.yaml`): `POST /v1/transfers` (idempotent), `GET /v1/transfers/{id}`.
Internal gRPC: receives rail callbacks; exposes a corrective endpoint consumed by the feedback loop.

### 2.3 mockrail
A deterministic-by-seed simulator of the NIP rail. Configurable (via env) probabilities for: added latency, hard timeout (no response), duplicate success callback, and explicit decline. It is the source of the chaos the switch must survive — the thing that manufactures the in-doubt states reconciliation later catches.

### 2.4 reconcile
A Cobra CLI. Reads the internal export and an external settlement file through adapters that normalize both into the canonical record, runs the matching engine, writes the exceptions report (text + JSON), and records the run. Streams input; deterministic output.

## 3. The canonical transaction record (the contract)

This is the load-bearing abstraction. Both the ledger export and every settlement-file adapter produce **this exact shape**, which is why matching is possible. It lives in `pkg/canonical` because it is shared by everything.

| Field | Type | Notes |
|---|---|---|
| `transaction_id` | UUID | Internal id. May be empty for external-only records. |
| `reference` | string | The cross-system key (NIP end-to-end / session reference). The primary match key. |
| `source` | string | Source account/bank identifier. |
| `destination` | string | Destination account/bank identifier. |
| `amount_minor` | int64 | Integer minor units (kobo). Never a float. |
| `currency` | string | ISO 4217, e.g. `NGN`. |
| `type` | enum | `transfer` \| `reversal` \| `fee`. |
| `status` | enum | `pending` \| `debited` \| `settled` \| `failed` \| `timed_out` \| `reversed`. |
| `initiated_at` | timestamp | UTC. |
| `settled_at` | timestamp | UTC, nullable. |
| `metadata` | map | Free-form; never used as a match key. |

Design rule: adapters are the only place that knows about messy external formats (fixed-width, odd date formats, scheme-specific reference fields). They map *into* the canonical record. Nothing downstream of an adapter ever sees a raw external row.

## 4. Transfer state machine

Every transfer is an explicit, externalized state machine. The whole point is that an in-doubt outcome (timeout/unknown) is a first-class state that routes to reversal — it is never silently treated as success or failure.

States: `INITIATED → DEBIT_PENDING → DEBITED → AWAITING_SETTLEMENT → {SETTLED | REVERSAL_PENDING → REVERSED}`.

| From | Event | To | Side effect |
|---|---|---|---|
| INITIATED | idempotency reserved, validated | DEBIT_PENDING | persist state |
| DEBIT_PENDING | ledger debits source | DEBITED | balanced entry posted |
| DEBITED | rail call sent | AWAITING_SETTLEMENT | outbox event written |
| AWAITING_SETTLEMENT | rail success | SETTLED | credit destination, mark settled |
| AWAITING_SETTLEMENT | rail decline | REVERSAL_PENDING | enqueue reversal |
| AWAITING_SETTLEMENT | **timeout / unknown** | REVERSAL_PENDING | enqueue reversal (in-doubt → reverse) |
| REVERSAL_PENDING | compensating entries posted | REVERSED | source restored |
| SETTLED / REVERSED | duplicate callback | (no change) | idempotent no-op |

`SETTLED` and `REVERSED` are terminal. A crash in any non-terminal state is recoverable: state is in Postgres, and the outbox poller resumes pending work on restart.

## 5. Consistency model & key patterns

- **Integer money.** All amounts are `int64` kobo via a `pkg/money` type. No floats anywhere near a balance. (ADR-0001)
- **Serializable ledger writes.** `PostTransaction` runs at `SERIALIZABLE` isolation; the balanced-entry check and the (optional) cached-balance update happen in the same transaction. (ADR-0002)
- **Idempotency keys.** The switch stores each `Idempotency-Key` with a request fingerprint and the produced result; a replay returns the stored result. Keys live in Postgres (durable) with a Redis fast-path for hot lookups. (ADR-0003)
- **Transactional outbox.** State changes and the events that must follow them (rail calls, reversals) are written in one DB transaction to an `outbox` table; a poller publishes them. This is what makes "no stranded debit" survive crashes — there is no window where a debit is committed but its follow-up is lost. (ADR-0004)
- **Reversal as compensation.** Money is never "un-debited" by editing the journal. A reversal is a new, parent-linked transaction posting the inverse entries. Append-only stays append-only.

## 6. Reconciliation design

- **Match keys & tolerances.** Primary key is `reference`; secondary checks are `amount_minor` (exact, since it is integer) and `initiated_at` within a configurable window. Tolerances are config, not code.
- **Exception categories** (`internal/reconcile/exceptions.go`):
  - `unmatched_internal` — in the ledger, absent from settlement (potential leak or timing).
  - `unmatched_external` — in settlement, absent from the ledger (owed credit or duplicate).
  - `amount_mismatch` — both present, amounts differ (fee/partial-settlement).
  - `pending_reversal` — ledger shows a timed-out/failed transfer whose reversal has not settled. **This is the category that feeds the loop back to the switch.**
  - `duplicate` — the same reference appears more than once on a side.
- **Determinism.** Internally the matcher may fan out across goroutines, but it sorts and keys deterministically so output is independent of row order and worker scheduling. (FR-C6)
- **Recorded runs.** Each run writes a `recon_runs` row and its `recon_exceptions`; reports are reproducible and auditable.
- **Streaming.** Inputs are read as streams; the working index is keyed by `reference`, not the whole file held in memory. (FR-C7)

## 7. Observability

- **Logging:** `slog` (stdlib), JSON handler, every line carries a `correlation_id` propagated from the transfer request through rail callbacks and ledger calls.
- **Metrics:** `prometheus/client_golang`. Key SLIs: transfer outcome counters by terminal state, reversal latency histogram, outbox lag gauge, reconciliation matched/exception counters per run.
- **Tracing:** OpenTelemetry → Jaeger (local). A transfer is one trace spanning switch → rail → ledger.
- **The portfolio payoff:** the dashboard that shows zero stranded debits under a chaos run, and the trace of an in-doubt transfer becoming a completed reversal, are concrete artifacts to write about.

## 8. Tools & tech stack

| Concern | Choice | Why |
|---|---|---|
| Language | Go 1.22+ | Concurrency model + single-binary deploy suit payments infra and low-infrastructure environments. |
| Database | PostgreSQL 16 | Serializable transactions, strong constraints, JSONB for metadata. |
| Cache / fast state | Redis 7 | Idempotency fast-path, ephemeral counters. |
| Inter-service RPC | gRPC + protobuf, codegen via `buf` | Typed contracts between ledger and switch. |
| Public API | REST via `chi` | Simple, idiomatic HTTP for the transfer endpoint; OpenAPI spec. |
| DB access | `sqlc` + `pgx` | Type-safe queries generated from SQL; no heavy ORM hiding the cost of money operations. |
| Migrations | `golang-migrate` | Versioned, reversible schema. |
| CLI | `cobra` + `viper` | Standard for Go tooling; config from flags/env/file. |
| Messaging (feedback) | outbox + poller; optional NATS | Start with the outbox; add a broker only if needed. |
| Logging | `slog` | Structured, in the stdlib. |
| Metrics | `prometheus/client_golang` | De facto standard. |
| Tracing | OpenTelemetry + Jaeger | End-to-end transfer traces. |
| Testing | `testing` + `testify`, `testcontainers-go`, a property lib (`rapid`) | Real-Postgres integration tests; property-based ledger invariant. |
| Load | `k6` | Throughput/latency against NFR targets. |
| Lint | `golangci-lint` | Consistent quality gate. |
| Local stack | Docker Compose | One command to boot postgres/redis/jaeger. |
| CI | GitHub Actions | Lint + unit + integration on PR. |

## 9. Folder structure

Idiomatic Go layout. The split that matters: `cmd/` holds thin entrypoints, `internal/` holds private application code the compiler forbids outsiders from importing, and `pkg/` holds the few genuinely shareable libraries — most importantly the canonical contract.

```
invariantcore/
├── README.md
├── go.mod  go.sum
├── Makefile
├── docker-compose.yml
├── .env.example
├── .golangci.yml
├── api/
│   ├── proto/
│   │   ├── ledger/v1/ledger.proto
│   │   └── switch/v1/switch.proto
│   └── openapi/switch.yaml
├── cmd/                      # entrypoints only (wire deps, start server)
│   ├── ledger/main.go
│   ├── switchd/main.go
│   ├── mockrail/main.go
│   └── reconcile/main.go     # the Cobra CLI root
├── internal/                 # private; not importable outside this module
│   ├── ledger/
│   │   ├── account.go  entry.go  transaction.go
│   │   ├── service.go        # PostTransaction, GetBalance, invariant check
│   │   └── postgres/         # sqlc-generated + repository impl
│   ├── switch/
│   │   ├── statemachine.go   # the table in §4
│   │   ├── orchestrator.go   # drives transfers
│   │   ├── idempotency.go
│   │   └── outbox/           # writer + poller
│   ├── mockrail/             # chaos injection
│   └── reconcile/
│       ├── matcher.go        # the matching engine
│       ├── adapters/         # external format → canonical
│       │   ├── nibss.go  csv.go
│       ├── report.go         # text + json output
│       └── exceptions.go     # the categories
├── pkg/                      # shareable libraries
│   ├── canonical/transaction.go   # THE contract
│   ├── money/money.go             # int64 kobo type
│   ├── logging/  metrics/  tracing/
├── migrations/               # golang-migrate
│   ├── 0001_init.up.sql  0001_init.down.sql
├── db/schema.sql             # full reference schema (mirrors migrations)
├── deployments/
│   ├── Dockerfile  prometheus.yml
│   └── k8s/                  # optional
├── scripts/
│   ├── seed/main.go          # system + demo accounts
│   └── gen_settlement/main.go# generate settlement files w/ injected discrepancies
├── test/
│   ├── integration/          # testcontainers-backed
│   ├── chaos/                # no-stranded-debit scenario
│   └── load/transfers.js     # k6 script
└── docs/
    ├── PRD.md  ARCHITECTURE.md  ROADMAP.md
    ├── diagrams/data-flow.svg
    └── adr/
        ├── 0001-integer-money.md
        ├── 0002-serializable-ledger.md
        ├── 0003-idempotency-keys.md
        ├── 0004-transactional-outbox.md
        └── 0005-canonical-record.md
```

## 10. Architecture decision records (summaries)

Keep these as short markdown files under `docs/adr/`. Each is context → decision → consequences.

- **ADR-0001 — Integer minor units for money.** Floats lose pennies; money is `int64` kobo behind a `money.Amount` type. Consequence: all arithmetic is explicit; display formatting is a boundary concern.
- **ADR-0002 — Serializable ledger writes.** Balance correctness beats write throughput on the ledger path. Consequence: handle serialization-failure retries; benchmark to confirm it meets the throughput target.
- **ADR-0003 — Durable idempotency keys.** Keys persist in Postgres (Redis is a cache, not the record). Consequence: a replay after a cache flush is still correct.
- **ADR-0004 — Transactional outbox.** No dual-write between DB and the rail. Consequence: a poller and at-least-once delivery, so consumers must be idempotent.
- **ADR-0005 — One canonical record in `pkg/`.** A single shared shape both sides conform to. Consequence: external messiness is quarantined in adapters.

## 11. Testing strategy

- **Unit** — state-machine transitions, money arithmetic, adapter parsing.
- **Property-based** (`rapid`) — for thousands of random transaction sets, assert global conservation of balance (AC-2).
- **Integration** (`testcontainers-go`) — run against a real Postgres: serializable posting, idempotent replays, reversal recovery after a simulated restart.
- **Chaos** (`test/chaos`) — drive N transfers with mockrail injecting timeouts, duplicates, and a mid-flow kill; assert zero stranded debits (AC-1).
- **Reconciliation fixtures** — a generated pair with K injected discrepancies; assert 100% recall and correct categories (AC-3), and identical output on re-run (AC-4).
- **Load** (`k6`) — measure throughput and p99 against NFR-2/NFR-3.

## 12. Local development

`make dev` boots Postgres, Redis, and Jaeger via `docker-compose.yml`. `make migrate-up` applies migrations; `make seed` creates the system accounts (settlement/suspense, fees) and a couple of demo customer accounts. Run `ledger`, `mockrail`, and `switchd` in separate terminals (or add them to compose once stable), then exercise the REST API and the `reconcile` CLI. See the README quickstart.
