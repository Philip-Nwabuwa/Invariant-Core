#!/usr/bin/env bash
#
# NS-501/502 feedback-loop demo (AC-5) — proves reconcile closes the loop:
# a stranded pending_reversal is fed back to switchd, which re-drives the
# reversal through the existing outbox path, and the next reconcile run shows the
# pending_reversal resolved.
#
# It runs the real binaries (ledger + mockrail-declining + switchd) against the
# local stack, fires a transfer the rail declines, and deterministically STRANDS
# the reversal: the ledger is stopped so handleReversal cannot post, the transfer
# parks in reversal_pending, and its unpublished outbox event is deleted (a lost
# event / crash window). With the ledger back up, the reconcile CLI — pointed at
# the same gap via a crafted internal/external pair — calls switchd's
# CorrectiveReversal over gRPC, which re-enqueues the reversal; the poller then
# posts it and the source is restored. A second reconcile run (now seeing the
# settled reversal) reports zero pending_reversal.
#
# Prereqs (the script does NOT start these for you):
#   make dev && make migrate-up && make seed     # pg up, schema applied, CUST-001/002 seeded
#
# Usage:
#   ./scripts/feedback_loop_demo.sh
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

REF="feedback-demo-$(date +%s)-$$"
IDEMPOTENCY_KEY="$REF"
AMOUNT_MINOR=5000
SOURCE="CUST-001"
DEST="CUST-002"
NOW="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

LOG_DIR="$(mktemp -d)"
LEDGER_PID="" ; MOCKRAIL_PID="" ; SWITCHD_PID=""

# ---- helpers ----------------------------------------------------------------
log()  { printf '\033[1;34m[demo]\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m[PASS]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[FAIL]\033[0m %s\n' "$*" >&2; exit 1; }

psql_q() {
  docker compose exec -T postgres \
    psql -U invariantcore -d invariantcore -tA -c "$1" 2>/dev/null | tr -d '[:space:]'
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

cleanup() {
  for pid in "$SWITCHD_PID" "$MOCKRAIL_PID" "$LEDGER_PID"; do
    [ -n "$pid" ] && kill "$pid" >/dev/null 2>&1 || true
  done
  wait >/dev/null 2>&1 || true
}
trap cleanup EXIT

cust1_balance() { psql_q "SELECT COALESCE(balance_minor,0) FROM account_balances b JOIN accounts a ON a.id=b.account_id WHERE a.code='${SOURCE}';"; }

# ---- 0. preflight -----------------------------------------------------------
log "checking the local stack is up (make dev && make migrate-up && make seed)..."
docker compose exec -T postgres pg_isready -U invariantcore >/dev/null 2>&1 \
  || fail "postgres not reachable — run 'make dev && make migrate-up && make seed' first"
[ "$(psql_q "SELECT count(*) FROM accounts WHERE code='${SOURCE}';")" = "1" ] \
  || fail "${SOURCE} not seeded — run 'make seed' first"

# ---- 1. build + start ledger and a DECLINING mockrail -----------------------
log "building binaries..."
go build -o ./bin/ ./cmd/... >/dev/null

log "starting ledger..."
start_ledger

log "starting mockrail with MOCKRAIL_P_DECLINE=1.0 (every transfer is declined -> reversal)..."
MOCKRAIL_P_DECLINE=1.0 ./bin/mockrail >"$LOG_DIR/mockrail.log" 2>&1 &
MOCKRAIL_PID=$!
wait_health "http://localhost:8082/healthz" || fail "mockrail did not become healthy"

log "starting switchd..."
MOCKRAIL_GRPC_TARGET=localhost:50053 LEDGER_GRPC_TARGET=localhost:50051 \
  DB_URL="$DB_URL" SWITCHD_HTTP_ADDR="$SWITCHD_HTTP_ADDR" \
  ./bin/switchd >"$LOG_DIR/switchd.log" 2>&1 &
SWITCHD_PID=$!
wait_health "${BASE}/healthz" || fail "switchd did not become healthy (see $LOG_DIR/switchd.log)"

BAL_BEFORE="$(cust1_balance)"
log "${SOURCE} balance before = ${BAL_BEFORE}"

# ---- 2. fire a transfer, then stop the ledger to strand the reversal --------
log "POST /v1/transfers (ref=${REF})..."
RESP="$(curl -fsS -X POST "${BASE}/v1/transfers" \
  -H 'Content-Type: application/json' \
  -H "Idempotency-Key: ${IDEMPOTENCY_KEY}" \
  -d "{\"source\":\"${SOURCE}\",\"destination\":\"${DEST}\",\"amount_minor\":${AMOUNT_MINOR},\"currency\":\"NGN\",\"reference\":\"${REF}\"}")"
TRANSFER_ID="$(echo "$RESP" | jq -r '.id')"
[ -n "$TRANSFER_ID" ] && [ "$TRANSFER_ID" != "null" ] || fail "no transfer id in response: $RESP"
log "transfer ${TRANSFER_ID} created (debit posted); stopping the ledger so the reversal cannot post"

# Stop the ledger NOW: the debit is already posted, but handleReversal needs the
# ledger, so the transfer will reach reversal_pending and stay there.
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

# ---- 3. strand it: delete the unpublished reversal event --------------------
log "stranding the reversal: deleting its unpublished outbox event"
psql_q "DELETE FROM outbox WHERE aggregate_id='${TRANSFER_ID}' AND published_at IS NULL;" >/dev/null
LEFT="$(psql_q "SELECT count(*) FROM outbox WHERE aggregate_id='${TRANSFER_ID}' AND published_at IS NULL;")"
[ "$LEFT" = "0" ] || fail "expected 0 unpublished outbox events after strand, got ${LEFT}"
ok "transfer ${TRANSFER_ID} is stranded in reversal_pending with no driving event"

# ---- 4. bring the ledger back; reconcile feeds the gap to switchd -----------
log "restarting ledger..."
start_ledger

INT_FILE="$LOG_DIR/internal.jsonl"
EXT_FILE="$LOG_DIR/external.csv"
# Internal export: the transfer recorded as failed (timed-out/declined), no
# settled reversal yet -> reconcile classifies it pending_reversal.
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

# ---- 5. assert the re-reversal fired ---------------------------------------
log "polling for the transfer to reach REVERSED..."
FINAL=""
for _ in $(seq 1 60); do
  S="$(psql_q "SELECT status FROM transactions WHERE id='${TRANSFER_ID}';")"
  [ "$S" = "reversed" ] && { FINAL="$S"; break; }
  sleep 0.5
done
[ "$FINAL" = "reversed" ] || fail "transfer never reached reversed after corrective feedback (see $LOG_DIR/switchd.log)"

REV_ROWS="$(psql_q "SELECT count(*) FROM transactions WHERE reference='${REF}' AND type='reversal';")"
[ "$REV_ROWS" = "1" ] || fail "expected exactly 1 reversal row, got ${REV_ROWS}"
BAL_AFTER="$(cust1_balance)"
[ "$BAL_AFTER" = "$BAL_BEFORE" ] || fail "source not restored: before=${BAL_BEFORE} after=${BAL_AFTER}"
ok "re-reversal fired via reconcile feedback: transfer REVERSED, source restored (${BAL_BEFORE} -> ${BAL_AFTER}), one reversal row"

# ---- 6. next reconcile run shows the pending_reversal resolved (AC-5) --------
# A fresh internal export now also carries the settled reversal for REF, so the
# pending_reversal is gone.
printf '{"reference":"%s","source":"%s","destination":"%s","amount_minor":%s,"currency":"NGN","type":"reversal","status":"settled","initiated_at":"%s"}\n' \
  "$REF" "$DEST" "$SOURCE" "$AMOUNT_MINOR" "$NOW" >> "$INT_FILE"

log "reconcile run #2 (now sees the settled reversal) — expect 0 pending_reversal"
RUN2="$(go run ./cmd/reconcile run --internal "$INT_FILE" --external "$EXT_FILE" \
  --switch-addr "$SWITCH_GRPC" --no-persist --format json 2>"$LOG_DIR/recon2.err")"
cat "$LOG_DIR/recon2.err"
PR2="$(echo "$RUN2" | jq -r '.by_category.pending_reversal // 0')"
[ "$PR2" = "0" ] || fail "run #2 expected 0 pending_reversal (resolved), got ${PR2}"

ok "AC-5: the pending_reversal was fed back, re-reversed, and resolved on the next run."
log "logs: $LOG_DIR"
