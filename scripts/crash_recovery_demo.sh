#!/usr/bin/env bash
#
# NS-306 crash-recovery demo — proves no work is lost when switchd dies mid-flow.
#
# It runs the real binaries (ledger + mockrail + switchd) against the local
# stack, fires a transfer, kill -9's switchd while the transfer is mid-flow
# (debited, awaiting settlement behind an injected rail latency), then restarts
# switchd. Its boot-time recovery sweep + outbox poller resume the transfer to a
# terminal state. We assert the transfer settles and that the source was debited
# exactly once — a stranded or doubled debit fails the script.
#
# Prereqs (the script does NOT start these for you):
#   make dev && make migrate-up && make seed     # pg up, schema applied, CUST-001/002 seeded
#
# Usage:
#   ./scripts/crash_recovery_demo.sh
#
set -euo pipefail

# ---- config -----------------------------------------------------------------
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

DB_URL="${DB_URL:-postgres://invariantcore:invariantcore@localhost:5432/invariantcore?sslmode=disable}"
SWITCHD_HTTP_ADDR="${SWITCHD_HTTP_ADDR:-:8080}"
HTTP_PORT="${SWITCHD_HTTP_ADDR##*:}"
BASE="http://localhost:${HTTP_PORT}"

# A long rail latency guarantees a real mid-flow window: after the synchronous
# debit, the poller's rail call blocks here long enough for us to land the kill.
RAIL_LATENCY_MS="${RAIL_LATENCY_MS:-8000}"

REF="recovery-demo-$(date +%s)-$$"
IDEMPOTENCY_KEY="$REF"
AMOUNT_MINOR=5000
SOURCE="CUST-001"
DEST="CUST-002"

LOG_DIR="$(mktemp -d)"
LEDGER_PID="" ; MOCKRAIL_PID="" ; SWITCHD_PID=""

# ---- helpers ----------------------------------------------------------------
log()  { printf '\033[1;34m[demo]\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m[PASS]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[FAIL]\033[0m %s\n' "$*" >&2; exit 1; }

psql_q() { # run a single SQL query inside the postgres container, value-only
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

start_switchd() {
  MOCKRAIL_GRPC_TARGET=localhost:50053 \
  LEDGER_GRPC_TARGET=localhost:50051 \
  DB_URL="$DB_URL" SWITCHD_HTTP_ADDR="$SWITCHD_HTTP_ADDR" \
    ./bin/switchd >"$LOG_DIR/switchd.log" 2>&1 &
  SWITCHD_PID=$!
  wait_health "${BASE}/healthz" || fail "switchd did not become healthy (see $LOG_DIR/switchd.log)"
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

# ---- 1. build + start ledger and mockrail (mockrail with injected latency) --
log "building binaries..."
go build -o ./bin/ ./cmd/... >/dev/null

log "starting ledger..."
DB_URL="$DB_URL" ./bin/ledger >"$LOG_DIR/ledger.log" 2>&1 &
LEDGER_PID=$!
wait_health "http://localhost:8081/healthz" || fail "ledger did not become healthy"

log "starting mockrail with MOCKRAIL_LATENCY_MS=${RAIL_LATENCY_MS} (the mid-flow window)..."
MOCKRAIL_LATENCY_MS="$RAIL_LATENCY_MS" ./bin/mockrail >"$LOG_DIR/mockrail.log" 2>&1 &
MOCKRAIL_PID=$!
wait_health "http://localhost:8082/healthz" || fail "mockrail did not become healthy"

# ---- 2. start switchd, fire a transfer --------------------------------------
log "starting switchd..."
start_switchd

log "POST /v1/transfers (ref=${REF})..."
RESP="$(curl -fsS -X POST "${BASE}/v1/transfers" \
  -H 'Content-Type: application/json' \
  -H "Idempotency-Key: ${IDEMPOTENCY_KEY}" \
  -d "{\"source\":\"${SOURCE}\",\"destination\":\"${DEST}\",\"amount_minor\":${AMOUNT_MINOR},\"currency\":\"NGN\",\"reference\":\"${REF}\"}")"
TRANSFER_ID="$(echo "$RESP" | jq -r '.id')"
STATE="$(echo "$RESP" | jq -r '.state')"
[ -n "$TRANSFER_ID" ] && [ "$TRANSFER_ID" != "null" ] || fail "no transfer id in response: $RESP"
log "transfer ${TRANSFER_ID} created, state=${STATE} (debit posted; settlement pending behind the rail latency)"

# ---- 3. kill switchd mid-flow ----------------------------------------------
log "kill -9 switchd (PID ${SWITCHD_PID}) MID-FLOW — before settlement completes"
kill -9 "$SWITCHD_PID" >/dev/null 2>&1 || true
wait "$SWITCHD_PID" 2>/dev/null || true
SWITCHD_PID=""

# Confirm we really crashed mid-flow: the lifecycle row is not yet terminal.
MIDSTATE="$(psql_q "SELECT status FROM transactions WHERE id='${TRANSFER_ID}';")"
log "post-crash DB status = ${MIDSTATE}"
case "$MIDSTATE" in
  settled|reversed) fail "transfer already terminal before restart — increase RAIL_LATENCY_MS to widen the mid-flow window";;
esac

# ---- 4. restart switchd; recovery + poller resume the work ------------------
log "restarting switchd — boot recovery sweep + poller should resume the stranded transfer"
start_switchd
if grep -q "recovery re-enqueued" "$LOG_DIR/switchd.log"; then
  log "recovery sweep re-enqueued the stranded transfer at boot"
fi

# ---- 5. assert it reaches a terminal state, debited exactly once ------------
log "polling GET /v1/transfers/${TRANSFER_ID} for a terminal state (rail lease resume can take ~30s)..."
FINAL=""
for _ in $(seq 1 80); do
  S="$(curl -fsS "${BASE}/v1/transfers/${TRANSFER_ID}" | jq -r '.state')"
  case "$S" in
    SETTLED|REVERSED|MANUAL_REVIEW|FAILED) FINAL="$S"; break;;
  esac
  sleep 1
done
[ -n "$FINAL" ] || fail "transfer never reached a terminal state (see $LOG_DIR/switchd.log)"
log "terminal state = ${FINAL}"

[ "$FINAL" = "SETTLED" ] || fail "expected SETTLED after recovery, got ${FINAL}"

DEBIT_LEGS="$(psql_q "SELECT count(*) FROM transactions WHERE reference='${REF}' AND idempotency_key LIKE '%:debit';")"
[ "$DEBIT_LEGS" = "1" ] || fail "expected exactly 1 debit leg (no double debit), got ${DEBIT_LEGS}"

ok "transfer resumed to SETTLED after a mid-flow crash; source debited exactly once — no work lost."
log "logs: $LOG_DIR"
