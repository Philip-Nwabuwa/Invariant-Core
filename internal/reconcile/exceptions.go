// Package reconcile matches an internal ledger export against an external
// settlement file and categorizes every gap between the two (FR-C). It is the
// after-the-fact proof that internal and external truth agree.
package reconcile

import "github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"

// Category is the kind of reconciliation gap an exception represents. The five
// values mirror the recon_exceptions CHECK constraint in db/schema.sql exactly.
type Category string

// Exception categories (FR-C3).
const (
	// CategoryUnmatchedInternal: an internal record with no external counterpart.
	CategoryUnmatchedInternal Category = "unmatched_internal"
	// CategoryUnmatchedExternal: an external record with no internal counterpart.
	CategoryUnmatchedExternal Category = "unmatched_external"
	// CategoryAmountMismatch: references match but amounts differ.
	CategoryAmountMismatch Category = "amount_mismatch"
	// CategoryPendingReversal: an internal failed/timed-out transfer whose
	// reversal has not yet settled — feeds the Sprint 5 corrective loop.
	CategoryPendingReversal Category = "pending_reversal"
	// CategoryDuplicate: the same reference appears more than once externally.
	CategoryDuplicate Category = "duplicate"
)

// Exception is one categorized gap. Internal/External hold the canonical records
// from each side (either may be nil depending on the category); DeltaMinor is set
// only for amount mismatches (external_minor − internal_minor).
type Exception struct {
	Category   Category          `json:"category"`
	Reference  string            `json:"reference"`
	Internal   *canonical.Record `json:"internal_record,omitempty"`
	External   *canonical.Record `json:"external_record,omitempty"`
	DeltaMinor *int64            `json:"delta_minor,omitempty"`
}

// classifyUnmatchedInternal decides whether a leftover internal record (one with
// no external settlement) is a pending reversal or simply unmatched. A transfer
// that failed or timed out is money that should be flowing back to the source; if
// no settled reversal for its reference exists in the internal set yet, it is a
// pending_reversal (the reversal is still owed). Anything else is unmatched.
func classifyUnmatchedInternal(rec canonical.Record, settledReversals map[string]bool) Category {
	if rec.Type == canonical.TypeTransfer &&
		(rec.Status == canonical.StatusFailed || rec.Status == canonical.StatusTimedOut) &&
		!settledReversals[rec.Reference] {
		return CategoryPendingReversal
	}
	return CategoryUnmatchedInternal
}
