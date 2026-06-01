# Detection feeding correction: when reconcile finds a stranded reversal, the switch fixes it

*Build log · after Sprint 5 (feedback + hardening)*

The CBN gives you **24 hours** to reverse a failed NIP transfer before the
**₦10,000-per-item** penalty bites. Sprint 4 built a reconciler that can *find* a
stranded reversal — a customer debit whose refund never settled. But finding it is
only half the job. A discrepancy that sits in a report until a human reads the
report is, for the 24-hour clock, the same as not finding it at all. Sprint 5
closes the loop: **detection feeds correction, automatically.**

### The decision: reconcile talks back to the switch

When reconcile classifies an exception as `pending_reversal`, it doesn't just log
it. If pointed at the switch (`--switch-addr`), it calls a corrective gRPC RPC,
`CorrectiveReversal`, carrying the offending reference. The switch consumes that
and re-drives the reversal *through the exact same outbox path* the original
reversal would have taken — `RequeueReversal` re-appends the `reversal.requested`
event, the running poller re-runs the already-idempotent handler, and the source
is restored. The next reconcile run reports the exception resolved.

The thing I'm most pleased with is that this added almost no new *trusted*
machinery. Re-reversal reuses the idempotent reversal handler from Sprint 3, the
outbox from Sprint 3, and the exception classifier from Sprint 4. The corrective
call only *re-enqueues* when the transfer is genuinely in `reversal_pending`;
already-reversed or any other status is a `requeued=false` no-op, and an unknown
reference is a clean `NotFound`. Because every handler is idempotent under the
outbox's at-least-once delivery, a corrective call that races the poller is safe.

### The alternative I rejected: a human and a ticket

The conventional answer is an ops queue: reconcile files an exception, a human
triages it, someone clicks "reverse." That's appropriate when a reversal needs
judgment — and in a real deployment this loop *should* be approval-gated, which I
note explicitly in the design notes; the auto-close here is a sandbox affordance.
But for the mechanical, unambiguous case — "this debit's refund never posted,
post it" — routing it through a human adds hours to a 24-hour clock for no
decision that a machine can't make safely. The interesting engineering is making
the automatic path *safe* (idempotent, guarded, no-op on the wrong state), not
making it manual.

### The artifact

`make demo` runs the whole story end-to-end against the real binaries: fire
transfers through a chaotic rail, prove zero stranded debits, then deliberately
strand a reversal, run reconcile, watch it fire `CorrectiveReversal`
(`requeued=true`), and see the transfer reach `REVERSED` with the source restored
to the kobo — and the **second reconcile run report `pending_reversal=0`** (AC-5).
Detection in, correction out, on the same rails.

The hardening that shares this sprint matters too: under load the shared
`SETTLEMENT` suspense account contends at `SERIALIZABLE`, and the bounded
serialization-retry loop degrades gracefully into `503`/`Retry-After` backpressure
rather than corrupting a balance — latency grows, invariants don't break. The
serialization-retry rate is the headline panel on the dashboard.

**A question for other engineers:** which of your monitoring alerts merely *tell a
human* something is wrong, and which of those could safely *fix it* — and what
would you need to trust before you let them?
