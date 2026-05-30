# ADR-0001 — Integer minor units for money

**Status:** Accepted (Sprint 0)

## Context
Monetary values handled with floating-point types accumulate rounding error;
fractions of a kobo lost on arithmetic become real discrepancies a ledger can
never reconcile. The system's whole premise is that money is conserved exactly.

## Decision
All monetary amounts are `int64` counts of minor units (kobo), behind a
`money.Amount` type (`pkg/money`). No `float` ever represents or transports a
balance. Converting to a human-readable major-unit string is a display-boundary
concern handled by `Amount.String`; JSON encodes the bare integer minor units.

## Consequences
- All arithmetic is explicit and exact; overflow is the only failure mode and is
  far outside realistic amounts for an int64 kobo value.
- Display formatting (and any future currency-symbol rendering) lives at the
  boundary, never in storage.
- Multi-currency value comparison is out of scope for v1 (see ADR notes and
  DESIGN-NOTES #3 on the currency-agnostic balance check).
