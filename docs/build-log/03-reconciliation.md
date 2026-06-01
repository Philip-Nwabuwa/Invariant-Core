# The least glamorous code in fintech decides whether you get your money back

*Build log · after Sprint 4 (reconcile CLI)*

Across **11.2 billion** NIP transactions in 2024, the interesting failures aren't
the loud ones. The loud ones page someone. The dangerous ones are *silent*: the
switch believes a transfer settled, the settlement file from the rail disagrees,
and nobody notices until a customer or an auditor does the subtraction. Whether
you get your money back often comes down to the least glamorous program in the
building — the reconciler that, after the fact, compares what the system *thinks*
happened against what *actually* settled.

### The decision: one canonical record, deterministic matching, streaming

The reconciler has to be three things, and each is a deliberate choice.

**Trustworthy.** Internal truth (the ledger export) and external truth (a
NIBSS-style settlement file, or a generic CSV) arrive in different, messy shapes.
Both are normalized — by adapters, the *only* place that knows about external
formats — into one shared `canonical.Record` (ADR-0005). Nothing downstream of an
adapter ever sees a raw row. Matching keys on `reference`, then requires an exact
amount and currency within a configurable timestamp window. Disagreements aren't
swept up; they're *categorized*: `unmatched_internal`, `unmatched_external`,
`amount_mismatch`, `duplicate`, and `pending_reversal` (a failed transfer whose
reversal hasn't settled — the category that feeds Sprint 5's auto-correction).

**Reproducible.** Run it twice on the same inputs and you get a byte-identical
report and zero new exception rows. Output is sorted deterministically by
`(category, reference, delta)` so row order and worker scheduling can't change it,
and persistence is guarded by a streamed SHA-256 fingerprint of the input pair so
a re-run never double-counts. A reconciliation you can't trust to be stable is one
you'll quietly stop trusting.

**Fast.** Settlement files are large. The external side streams through a `Stream`
interface one record at a time; only the keyed internal index lives in memory.

### The alternative I rejected: load both files and diff them

The naive version slurps both files into memory and diffs. It's fine on a fixture
and falls over on a real day's volume — and worse, an order-dependent or
hash-of-the-whole-file approach makes "did anything change since yesterday" an
unanswerable question. I also rejected trusting the switch's own view as ground
truth: the entire point of reconciliation is that the switch can be *wrong*, and
the external file is the independent check.

### The artifact

A fixture generator injects K discrepancies spanning every category;
`TestFixture_FullRecall` asserts **100% recall** with the exact category label for
each injected reference and no spurious extras. And the speed claim is real:
**200,000 transactions per side reconciled in under one second**, streaming, with
only the keyed index held in memory.

**A question for other engineers:** if your system's internal view and an external
source of truth silently diverged today, how long until someone noticed — and
would the tool that finds it give the same answer twice?
