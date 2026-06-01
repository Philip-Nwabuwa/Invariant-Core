# How I built a ledger that *can't* lose a kobo

*Build log · after Sprint 1 (ledger core)*

In 2024, NIBSS Instant Payments moved roughly **₦1.07 quadrillion** across 11.2
billion transactions. At that scale a rounding bug isn't a rounding bug — it's a
reconciliation team, a regulator, and a customer whose ₦5,000 left their account
and arrived nowhere. So the first thing I built wasn't a transfer API. It was a
double-entry ledger whose core invariant I could *prove*, not just hope for:
**every transaction's debits equal its credits, and money is never created or
destroyed.**

### The decision: money is an integer, and the journal is append-only

Two choices fall out of "prove it can't lose a kobo."

First, **money is `int64` minor units (kobo), never a float** (ADR-0001). A
`float64` can't represent ₦0.10 exactly; do a few thousand of those and you've
quietly minted or burned fractions of a kobo that no ledger can ever reconcile.
Amounts live behind a `money.Amount` type — addition, subtraction, negation, and
a display-boundary `String()`, but no floating point ever touches a balance.

Second, **the journal is append-only**. A balance is not a column you update; it
is a *derivation* — the sum of entries against an account, applying its
normal-balance direction. A DB trigger rejects any `UPDATE` or `DELETE` on
`entries`. You can't fix a mistake by editing history; you post a compensating
entry. That constraint is annoying exactly until the day someone asks "why is
this balance wrong," and the answer is the entire, immutable, replayable truth.

### The alternative I rejected: trust example-based tests

The easy path is a handful of unit tests — "post a transfer, assert the two
balances." That proves the cases I thought of. It says nothing about the
thousands of interleavings I *didn't* think of. For an invariant, examples are
the wrong tool.

So conservation is a **property-based test** (`pgregory.net/rapid`, AC-2): generate
random *balanced* transaction sets across N seeded accounts, post them all
against a real Postgres, and assert the sum of every account balance equals the
starting total. Value conserved, across whatever the generator throws at it. A
companion property asserts that *unbalanced* sets are always rejected and never
commit. The satisfying part of writing it was watching it fail first — it caught
a real ordering bug — and then go green and stay green.

The other half is concurrency. Posts run at PostgreSQL **`SERIALIZABLE`**
isolation with the balance check inside the same transaction (ADR-0002). Weaker
levels admit read-skew anomalies that silently violate conservation. Serializable
makes correctness the default and turns contention into an explicit, retryable
`40001` error rather than a corrupt balance — a trade I'll happily make on the
ledger path, and one that comes back as a real throughput question in Sprint 5.

### The artifact

The proof is the failing-then-passing conservation property and the append-only
trigger test, both running against an ephemeral Postgres in CI. Balances are
reconstructible purely from the journal — drop the cache and re-derive, the
numbers are identical.

**A question for other engineers:** where in your system is a financial (or
otherwise sacred) invariant currently defended only by example-based tests — and
what would it take to express it as a property instead?
