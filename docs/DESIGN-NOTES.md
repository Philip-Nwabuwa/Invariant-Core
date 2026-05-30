# Design Notes — refinements & known edges

A companion to [ARCHITECTURE.md](ARCHITECTURE.md). The main docs describe the system as designed; this file records five places where the design has a sharper answer than the headline text implies, or a deliberate edge worth naming out loud. Each note is recorded as a known, intentional decision — not a TODO discovered late. Format: **Issue / Why it matters / Decision for v1 / Follow-up**.

---

## 1. In-doubt resolution: query before reversing

**Issue.** PRD FR-T4 and the state machine in ARCHITECTURE §4 route a `timeout / unknown` outcome straight to `REVERSAL_PENDING`.

**Why it matters.** Unknown is not the same as didn't-happen. A timeout means we lost the *answer*, not necessarily the *transfer*. If the rail actually settled and we reverse anyway, we strand money in the other direction — the debit is restored while the beneficiary keeps the credit. "Reverse on timeout" trades one stranding failure for another.

**Decision for v1.** Insert a status-query step before any in-doubt reversal. From `AWAITING_SETTLEMENT`, a timeout/unknown moves to an in-doubt state that issues a mocked **TSQ (Transaction Status Query)** against `mockrail`; only a rail-confirmed *no settlement / failed* result routes on to `REVERSAL_PENDING`. A confirmed success routes to `SETTLED`. `mockrail` gains a TSQ response (seedable, and able to disagree with the original outcome) so the chaos test exercises the "settled-but-we-timed-out" case explicitly. This is both closer to how real NIP resolves in-doubt items and a stronger demo than blind reversal.

**Follow-up.** Update the §4 transition table and FR-T4 when implemented; add the TSQ surface to `internal/mockrail`. Implementation deferred — Sprint 3 (NS-303) is the natural home.

## 2. Serializable writes meet a shared suspense account

**Issue.** Every transfer posts against the `SETTLEMENT` account, and the cached `account_balances` row for it is updated inside the same `SERIALIZABLE` transaction (schema.sql lines 103–108; ADR-0002).

**Why it matters.** That single row is a contention magnet. Under concurrency, every transfer serializes on the suspense account's cached balance, producing a stream of serialization failures and retries. The 500 tps single-node target (NFR-3) will be gated by this hotspot, not by Postgres itself — the bottleneck is one row, not the database.

**Decision for v1.** Treat the **serialization-retry rate on system accounts** as a first-class SLI alongside outbox lag (extends NS-308 / NS-505), and be explicit in ADR-0002 that this is the number that determines whether the throughput target is reachable. Mitigations held in reserve rather than built up front: shard the suspense account into N sub-accounts and pick by hash, or drop the cached balance for hot system accounts and derive their balance from the journal on read.

**Follow-up.** Confirm the contention story with the k6 run (NS-504); only add sharding if the measured retry rate forces it. Don't pre-optimize.

## 3. The balance invariant is not currency-aware

**Issue.** `assert_transaction_balanced()` (schema.sql lines 76–96) sums `amount_minor` across all of a transaction's entries regardless of each entry's `currency`.

**Why it matters.** With NGN-only v1 this is correct and cheap. But the moment a transaction mixes currencies, a balanced-by-number set could be unbalanced in value, and the trigger — our backstop invariant — would wave it through. It's a latent correctness hole, not a performance one.

**Decision for v1.** Record it and keep v1 single-currency (multi-currency/FX is out of scope per PRD §9). The trigger stays as-is, with a comment noting the assumption. When multi-currency lands, either group the balance check `BY currency` or add a CHECK enforcing one currency per transaction and balance per currency-group.

**Follow-up.** Revisit only if/when multi-currency leaves the out-of-scope list.

## 4. The auto-closed feedback loop is a sandbox affordance

**Issue.** FR-F1 and AC-5 have Reconcile automatically trigger a re-reversal in `switchd` when it finds a `pending_reversal`.

**Why it matters.** A detection tool that autonomously moves money is something you would not ship to production unguarded — the loop would be human-gated (an analyst approves the corrective action) precisely because the detector can be wrong. Left unstated, the auto-close reads as naïve rather than deliberate.

**Decision for v1.** Keep the loop auto-closed — it is the cleanest demonstration of detection feeding correction, and there is no real money at risk. Name it as a sandbox affordance: in a real deployment this becomes a queued, human-approved corrective action, and the switch's corrective endpoint would require an operator-authenticated request rather than firing on a recon exception alone.

**Follow-up.** None for v1; the corrective endpoint's design should leave room for an approval gate so the production shape is a config change, not a redesign.

## 5. Idempotency `in_progress` recovery

**Issue.** The `idempotency_keys` table carries an `in_progress` status and an `expires_at` (schema.sql lines 115–123), but the docs don't say what a *replay* sees when the original request crashed after writing `in_progress` but before reaching a terminal result.

**Why it matters.** Without a defined answer, a replay either blocks forever behind a dead `in_progress` row or risks double-processing — both are exactly the failure modes the idempotency layer exists to prevent.

**Decision for v1.** The transfer's durable state machine (in Postgres) is the source of truth, not the idempotency row. A replay of an `in_progress` key:
- within its lease window (`expires_at` not passed) returns "in progress, retry later" — the original may still be completing;
- past the lease re-attaches to the existing `transaction_id` and lets the outbox poller drive it to a terminal state, then returns that result.

A replay never starts a second transfer, because the key is unique and already bound to a `transaction_id`. This ties ADR-0003 (durable idempotency) to ADR-0004 (outbox-driven recovery): the same machinery that resumes pending work after a crash is what resolves a stranded `in_progress` key.

**Follow-up.** Specify the lease duration and the replay response contract in ADR-0003 when the idempotency store is built (Sprint 2, NS-202).
