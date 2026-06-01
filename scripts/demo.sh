#!/usr/bin/env bash
#
# NS-603 end-to-end portfolio demo — one script that tells the whole story:
#
#   1. fire a batch of transfers through a CHAOTIC rail (declines, timeouts,
#      duplicate callbacks, latency — deterministic by MOCKRAIL_SEED),
#   2. prove ZERO STRANDED DEBITS: every transfer reaches a terminal state and
#      every debit is matched by a credit or a completed reversal,
#   3. run the RECONCILE CLI against a crafted internal/external pair that
#      exposes a stranded `pending_reversal`,
#   4. let reconcile TRIGGER A RE-REVERSAL via switchd's CorrectiveReversal,
#   5. show the next reconcile run reports the exception RESOLVED (AC-5).
#
# It runs the real binaries (ledger + mockrail + switchd) against the local
# stack. Acts 1 and 4–5 reuse the mechanisms proven by scripts/crash_recovery_demo.sh
# (NS-306) and scripts/feedback_loop_demo.sh (NS-501/502); this is the unified
# narrative referenced from the README.
#
# Prereqs (the script does NOT start these for you):
#   make dev && make migrate-up && make seed     # pg up, schema applied, CUST-001/002 seeded
#
# Usage:
#   make demo            # or: ./scripts/demo.sh
#
set -euo pipefail

# ---- config -----------------------------------------------------------------
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

DB_URL="${DB_URL:-postgres://invariantcore:invariantcore@localhost:5432/invariantcore?sslmode=disable}"
SWITCHD_HTTP_ADDR="${SWITCHD_HTTP_ADDR:-:8080}"
HTTP_PORT="${SWITCHD_HTTP_ADDR##*:}"
BASE="http://localhost:${HTTP_PORT}"
SWITCH_GRPC="${SWITCH_GRPC:-localhost:50052}"

# Chaos batch (Act 1). Deterministic by seed; tuned so the batch drains quickly
# while still exercising the decline -> reversal and timeout -> TSQ paths.
N_CHAOS="${N_CHAOS:-12}"
MOCKRAIL_SEED="${MOCKRAIL_SEED:-42}"
CHAOS_PREFIX="chaos-demo-$(date +%s)-$$"

AMOUNT_MINOR=5000
SOURCE="CUST-001"
DEST="CUST-002"
NOW="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

LOG_DIR="$(mktemp -d)"
LEDGER_PID="" ; MOCKRAIL_PID="" ; SWITCHD_PID=""

# ---- helpers ----------------------------------------------------------------
log()  { printf '\033[1;34m[demo]\033[0m %s\n' "$*"; }
act()  { printf '\n\033[1;35m== %s ==\033[0m\n' "$*"; }
ok()   { printf '\033[1;32m[PASS]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[FAIL]\033[0m %s\n' "$*" >&2; exit 1; }

psql_q() { # single value, whitespace-stripped
  docker compose exec -T postgres \
    psql -U invariantcore -d invariantcore -tA -c "$1" 2>/dev/null | tr -d '[:space:]'
}
psql_table() { # human-readable table to stdout
  docker compose exec -T postgres \
    psql -U invariantcore -d invariantcore -c "$1" 2>/dev/null
}

wait_health() {
  local url="$1" tries=0
  until curl -fsS "$url" >/dev/null 2>&1; do
    tries=$((tries + 1))
    [ "$tries" -gt 50 ] && return 1
    sleep 0.2
  done
}

start_ledger() {
  DB_URL="$DB_URL" ./bin/ledger >"$LOG_DIR/ledger.log" 2>&1 &
  LEDGER_PID=$!
  wait_health "http://localhost:8081/healthz" || fail "ledger did not become healthy"
}

start_switchd() {
  MOCKRAIL_GRPC_TARGET=localhost:50053 LEDGER_GRPC_TARGET=localhost:50051 \
    DB_URL="$DB_URL" SWITCHD_HTTP_ADDR="$SWITCHD_HTTP_ADDR" \
    ./bin/switchd >"$LOG_DIR/switchd.log" 2>&1 &
  SWITCHD_PID=$!
  wait_health "${BASE}/healthz" || fail "switchd did not become healthy (see $LOG_DIR/switchd.log)"
}

# start_mockrail ENV...  — caller passes the chaos knobs as leading VAR=VAL args.
start_mockrail() {
  env "$@" ./bin/mockrail >"$LOG_DIR/mockrail.log" 2>&1 &
  MOCKRAIL_PID=$!
  wait_health "http://localhost:8082/healthz" || fail "mockrail did not become healthy"
}
stop_mockrail() {
  [ -n "$MOCKRAIL_PID" ] && kill "$MOCKRAIL_PID" >/dev/null 2>&1 || true
  wait "$MOCKRAIL_PID" 2>/dev/null || true
  MOCKRAIL_PID=""
}

cust1_balance() { psql_q "SELECT COALESCE(balance_minor,0) FROM account_balances b JOIN accounts a ON a.id=b.account_id WHERE a.code='${SOURCE}';"; }

post_transfer() { # $1=reference  -> echoes transfer id
  local ref="$1" resp
  resp="$(curl -fsS -X POST "${BASE}/v1/transfers" \
    -H 'Content-Type: application/json' \
    -H "Idempotency-Key: ${ref}" \
    -d "{\"source\":\"${SOURCE}\",\"destination\":\"${DEST}\",\"amount_minor\":${AMOUNT_MINOR},\"currency\":\"NGN\",\"reference\":\"${ref}\"}")"
  echo "$resp" | jq -r '.id'
}

cleanup() {
  for pid in "$SWITCHD_PID" "$MOCKRAIL_PID" "$LEDGER_PID"; do
    [ -n "$pid" ] && kill "$pid" >/dev/null 2>&1 || true
  done
  wait >/dev/null 2>&1 || true
}
trap cleanup EXIT

# ---- 0. preflight -----------------------------------------------------------
log "checking the local stack is up (make dev && make migrate-up && make seed)..."
docker compose exec -T postgres pg_isready -U invariantcore >/dev/null 2>&1 \
  || fail "postgres not reachable — run 'make dev && make migrate-up && make seed' first"
[ "$(psql_q "SELECT count(*) FROM accounts WHERE code='${SOURCE}';")" = "1" ] \
  || fail "${SOURCE} not seeded — run 'make seed' first"
command -v jq >/dev/null 2>&1 || fail "jq is required"

log "building binaries..."
go build -o ./bin/ ./cmd/... >/dev/null

log "starting ledger + switchd..."
start_ledger
start_switchd

# =============================================================================
act "ACT 1 — fire ${N_CHAOS} transfers through a CHAOTIC rail (seed=${MOCKRAIL_SEED})"
# =============================================================================
# A seeded rail: ~30% decline -> reversal, ~10% timeout -> IN_DOUBT -> TSQ,
# ~10% duplicate success callback (idempotent no-op). Reproducible per reference.
start_mockrail \
  MOCKRAIL_SEED="$MOCKRAIL_SEED" \
  MOCKRAIL_LATENCY_MS=50 \
  MOCKRAIL_P_DECLINE=0.3 \
  MOCKRAIL_P_TIMEOUT=0.1 \
  MOCKRAIL_P_DUPLICATE=0.1 \
  SWITCH_CALLBACK_TARGET="$SWITCH_GRPC"

log "POSTing ${N_CHAOS} transfers (prefix ${CHAOS_PREFIX})..."
for i in $(seq 1 "$N_CHAOS"); do
  id="$(post_transfer "${CHAOS_PREFIX}-${i}")"
  [ -n "$id" ] && [ "$id" != "null" ] || fail "transfer ${i} returned no id"
done

log "draining: waiting for every transfer to reach a terminal state..."
NONTERM=""
for _ in $(seq 1 90); do
  NONTERM="$(psql_q "SELECT count(*) FROM transactions
                     WHERE reference LIKE '${CHAOS_PREFIX}-%'
                       AND idempotency_key NOT LIKE '%:%'
                       AND status NOT IN ('settled','reversed','failed','manual_review');")"
  [ "$NONTERM" = "0" ] && break
  sleep 1
done

act "ACT 2 — prove ZERO STRANDED DEBITS"
log "outcome split:"
psql_table "SELECT status, count(*) AS transfers FROM transactions
            WHERE reference LIKE '${CHAOS_PREFIX}-%' AND idempotency_key NOT LIKE '%:%'
            GROUP BY status ORDER BY status;"

[ "$NONTERM" = "0" ] || fail "${NONTERM} transfer(s) left in a non-terminal (stranded) state — see $LOG_DIR/switchd.log"
ok "zero transfers left in a non-terminal state"

# Every transfer was debited exactly once (no doubled / missing debit).
DEBITS="$(psql_q "SELECT count(*) FROM transactions WHERE reference LIKE '${CHAOS_PREFIX}-%' AND idempotency_key LIKE '%:debit';")"
[ "$DEBITS" = "$N_CHAOS" ] || fail "expected ${N_CHAOS} debit legs (one per transfer), got ${DEBITS}"
ok "${DEBITS} debit legs for ${N_CHAOS} transfers — each debited exactly once"

# Every REVERSED transfer has its compensating reversal row (source restored);
# every SETTLED transfer has its settlement leg (beneficiary credited).
ORPHAN_REV="$(psql_q "SELECT count(*) FROM transactions t
  WHERE t.reference LIKE '${CHAOS_PREFIX}-%' AND t.idempotency_key NOT LIKE '%:%' AND t.status='reversed'
    AND NOT EXISTS (SELECT 1 FROM transactions r WHERE r.reference=t.reference AND r.type='reversal');")"
[ "$ORPHAN_REV" = "0" ] || fail "${ORPHAN_REV} reversed transfer(s) without a compensating reversal row"
ORPHAN_SET="$(psql_q "SELECT count(*) FROM transactions t
  WHERE t.reference LIKE '${CHAOS_PREFIX}-%' AND t.idempotency_key NOT LIKE '%:%' AND t.status='settled'
    AND NOT EXISTS (SELECT 1 FROM transactions s WHERE s.idempotency_key = t.id::text || ':settle');")"
[ "$ORPHAN_SET" = "0" ] || fail "${ORPHAN_SET} settled transfer(s) without a settlement leg"
ok "every debit matched by a credit or a completed reversal — NO STRANDED DEBITS under chaos"

# =============================================================================
act "ACT 3 — strand a reversal, then close the loop via RECONCILE → switchd"
# =============================================================================
# Switch to a fully-declining rail for one transfer and reproduce the
# feedback-loop scenario: strand it in reversal_pending, then let reconcile
# feed the gap back to switchd (CorrectiveReversal) and re-drive the reversal.
stop_mockrail
start_mockrail MOCKRAIL_P_DECLINE=1.0

REF="feedback-demo-$(date +%s)-$$"
BAL_BEFORE="$(cust1_balance)"
log "${SOURCE} balance before the stranded transfer = ${BAL_BEFORE}"

log "POST a transfer the rail declines (ref=${REF})..."
TRANSFER_ID="$(post_transfer "$REF")"
[ -n "$TRANSFER_ID" ] && [ "$TRANSFER_ID" != "null" ] || fail "no transfer id"
log "transfer ${TRANSFER_ID} created (debit posted); stopping the ledger so the reversal cannot post"

kill -9 "$LEDGER_PID" >/dev/null 2>&1 || true
wait "$LEDGER_PID" 2>/dev/null || true
LEDGER_PID=""

log "waiting for the transfer to park in reversal_pending..."
PARKED=""
for _ in $(seq 1 60); do
  S="$(psql_q "SELECT status FROM transactions WHERE id='${TRANSFER_ID}';")"
  [ "$S" = "reversal_pending" ] && { PARKED=1; break; }
  case "$S" in reversed|settled) fail "transfer reached ${S} before we could strand it";; esac
  sleep 0.5
done
[ -n "$PARKED" ] || fail "transfer never reached reversal_pending (see $LOG_DIR/switchd.log)"

log "stranding the reversal: deleting its unpublished outbox event"
psql_q "DELETE FROM outbox WHERE aggregate_id='${TRANSFER_ID}' AND published_at IS NULL;" >/dev/null
[ "$(psql_q "SELECT count(*) FROM outbox WHERE aggregate_id='${TRANSFER_ID}' AND published_at IS NULL;")" = "0" ] \
  || fail "outbox event not stranded"
ok "transfer ${TRANSFER_ID} is stranded in reversal_pending with no driving event"

log "restarting ledger..."
start_ledger

INT_FILE="$LOG_DIR/internal.jsonl"
EXT_FILE="$LOG_DIR/external.csv"
# Internal export: the transfer recorded as failed, no settled reversal yet ->
# reconcile classifies it pending_reversal.
printf '{"reference":"%s","source":"%s","destination":"%s","amount_minor":%s,"currency":"NGN","type":"transfer","status":"failed","initiated_at":"%s"}\n' \
  "$REF" "$SOURCE" "$DEST" "$AMOUNT_MINOR" "$NOW" > "$INT_FILE"
# External settlement: an unrelated settled row; REF is absent (no settlement).
{
  printf 'session_id,source_account,beneficiary_account,amount_kobo,currency,status,transaction_date\n'
  printf 'OTHER-REF,%s,%s,%s,NGN,settled,%s\n' "$SOURCE" "$DEST" "$AMOUNT_MINOR" "$NOW"
} > "$EXT_FILE"

log "reconcile run #1 with --switch-addr ${SWITCH_GRPC} (expect pending_reversal -> corrective call)"
RUN1="$(go run ./cmd/reconcile run --internal "$INT_FILE" --external "$EXT_FILE" \
  --switch-addr "$SWITCH_GRPC" --no-persist --format json 2>"$LOG_DIR/recon1.err")"
cat "$LOG_DIR/recon1.err"
PR1="$(echo "$RUN1" | jq -r '.by_category.pending_reversal // 0')"
[ "$PR1" = "1" ] || fail "run #1 expected 1 pending_reversal, got ${PR1}"
grep -q "requeued=true" "$LOG_DIR/recon1.err" || fail "reconcile did not re-enqueue the reversal (no requeued=true)"
ok "reconcile detected the gap and fired CorrectiveReversal (requeued=true)"

log "polling for the transfer to reach REVERSED..."
FINAL=""
for _ in $(seq 1 60); do
  S="$(psql_q "SELECT status FROM transactions WHERE id='${TRANSFER_ID}';")"
  [ "$S" = "reversed" ] && { FINAL="$S"; break; }
  sleep 0.5
done
[ "$FINAL" = "reversed" ] || fail "transfer never reached reversed after corrective feedback"
REV_ROWS="$(psql_q "SELECT count(*) FROM transactions WHERE reference='${REF}' AND type='reversal';")"
[ "$REV_ROWS" = "1" ] || fail "expected exactly 1 reversal row, got ${REV_ROWS}"
BAL_AFTER="$(cust1_balance)"
[ "$BAL_AFTER" = "$BAL_BEFORE" ] || fail "source not restored: before=${BAL_BEFORE} after=${BAL_AFTER}"
ok "re-reversal fired: transfer REVERSED, source restored (${BAL_BEFORE} -> ${BAL_AFTER}), one reversal row"

act "ACT 4 — the next reconcile run shows it RESOLVED (AC-5)"
# A fresh internal export now also carries the settled reversal for REF.
printf '{"reference":"%s","source":"%s","destination":"%s","amount_minor":%s,"currency":"NGN","type":"reversal","status":"settled","initiated_at":"%s"}\n' \
  "$REF" "$DEST" "$SOURCE" "$AMOUNT_MINOR" "$NOW" >> "$INT_FILE"
RUN2="$(go run ./cmd/reconcile run --internal "$INT_FILE" --external "$EXT_FILE" \
  --switch-addr "$SWITCH_GRPC" --no-persist --format json 2>"$LOG_DIR/recon2.err")"
PR2="$(echo "$RUN2" | jq -r '.by_category.pending_reversal // 0')"
[ "$PR2" = "0" ] || fail "run #2 expected 0 pending_reversal (resolved), got ${PR2}"
ok "AC-5: the pending_reversal was fed back, re-reversed, and resolved on the next run"

printf '\n'
ok "DEMO COMPLETE — zero stranded debits under chaos; detection fed correction and closed the loop."
log "logs: $LOG_DIR"
