# The reversal that stops a debit stranding a trader's money

*Build log · after Sprint 3 (reversals + resilience)*

A NIP-scale switch can be debiting customers at the rate of tens of billions of
naira a day. The CBN's instant-EFT regulation is blunt about what happens when one
of those transfers fails: reverse it within **24 hours** or eat a **₦10,000-per-item**
penalty — and that's the regulator's view; the customer's view is simpler and
angrier. These systems live or die on *consistency*, not uptime. A switch that's
up 99.99% of the time but occasionally leaves a debit with no matching credit has
failed at the only thing that matters.

### The decision: never assume, and never edit the journal

Two rules carry Sprint 3.

**Rule one: on a rail timeout, query before you decide.** When the rail times out
or replies "unknown," the dangerous instinct is to guess. Guess "success" and you
might never refund a customer whose money never moved. Guess "failure," reverse,
and the rail *did* settle — now you've double-spent. So the transfer goes to
`IN_DOUBT` and the switch issues a **Transaction Status Query** (TSQ) before
moving: TSQ-settled → settle; TSQ-no-settlement → reverse; inconclusive after
bounded retries → `MANUAL_REVIEW`, money parked in a suspense account where it is
*held*, never *lost*. `IN_DOUBT` is its own persisted state with its own outbox
event, so a crash mid-doubt re-issues the query — it never re-sends the payment.

**Rule two: a reversal is a compensating transaction, not an edit.** Restoring the
source is a brand-new, parent-linked ledger transaction posting the inverse
entries (ADR — append-only). Three guards make it idempotent — a status check, a
per-leg idempotency key, and a `unique` index of one reversal per parent — so
re-running a reversal is a no-op, not a second refund.

Holding it all together is a **transactional outbox** (ADR-0004): the state change
and the event that must follow it are written in *one* DB transaction. No
dual-write, so there's no window where the commit lands but the follow-up is lost.
A poller drains the outbox; on restart it simply resumes. That's the machinery
that makes "no stranded debit" survive a `kill -9`.

### The alternative I rejected: optimistic assume-and-go

The simpler design assumes the rail succeeded and moves on, reconciling
discrepancies later. It's faster to build and it's wrong in exactly the expensive
direction: the failure mode is a stranded customer debit, which is the one outcome
this whole project exists to prevent. Querying-before-deciding costs a round trip
and a state; stranding a trader's money costs trust and ₦10,000.

### The artifact

`test/chaos` drives 60 transfers through a deterministically chaotic rail
(timeouts, declines, duplicate callbacks) *plus* a mid-flow crash, and asserts the
headline guarantee: **zero stranded debits** — every transfer reaches its true,
seed-determined terminal state and balances reconcile exactly. The split is
reproducible across runs. The Grafana outcome panel shows the settled/reversed
split live, with reversal latency and outbox lag beside it.

**A question for other engineers:** when a downstream dependency answers "I don't
know," does your system have an explicit in-doubt state — or does it quietly pick
optimism or pessimism and hope?
