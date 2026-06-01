// k6 load test for POST /v1/transfers (NS-504, AC-6, NFR-2/3).
//
// Drives the switch's public REST endpoint with a unique Idempotency-Key and
// reference per iteration, so every request is genuine new work (no dedup). The
// POST debits synchronously and returns 202 at DEBITED; settlement is async via
// the outbox, so this latency excludes the injected rail delay (MOCKRAIL_LATENCY_MS)
// by construction — it measures the synchronous debit path.
//
// Targets (NFR-2/3): >=500 transfers/s, p99 < 250 ms.
//
// Usage (against a live local stack with seeded CUST-001/CUST-002):
//   make load                              # defaults below
//   RATE=200 DURATION=20s make load        # dial down for modest hardware
//   BASE_URL=http://host:8080 k6 run test/load/transfers.js
import http from "k6/http";
import { check } from "k6";

const BASE_URL = __ENV.BASE_URL || "http://localhost:8080";
const RATE = parseInt(__ENV.RATE || "500", 10); // transfers per second
const DURATION = __ENV.DURATION || "30s";
const SOURCE = __ENV.SOURCE || "CUST-001";
const DEST = __ENV.DEST || "CUST-002";

export const options = {
  scenarios: {
    transfers: {
      executor: "constant-arrival-rate",
      rate: RATE,
      timeUnit: "1s",
      duration: DURATION,
      // Headroom so the open model can sustain the arrival rate.
      preAllocatedVUs: Math.max(50, RATE),
      maxVUs: Math.max(200, RATE * 4),
    },
  },
  thresholds: {
    // NFR-3: tail latency of the synchronous debit path.
    http_req_duration: ["p(99)<250"],
    // Accepted transfers should be the overwhelming majority; transient 503
    // backpressure (NS-505) is tolerated in a small fraction under contention.
    "checks{check:accepted}": ["rate>0.99"],
  },
};

// uniqueId builds a per-request unique token without a network jslib import.
function uniqueId() {
  return `${__VU}-${__ITER}-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

export default function () {
  const id = uniqueId();
  const payload = JSON.stringify({
    source: SOURCE,
    destination: DEST,
    amount_minor: 100,
    currency: "NGN",
    reference: `load-${id}`,
  });
  const res = http.post(`${BASE_URL}/v1/transfers`, payload, {
    headers: {
      "Content-Type": "application/json",
      "Idempotency-Key": id,
    },
  });
  check(res, { accepted: (r) => r.status === 202 }, { check: "accepted" });
}
