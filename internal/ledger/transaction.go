package ledger

import (
	"github.com/google/uuid"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// PostRequest is a request to record one logical money movement as a balanced
// set of journal entries.
type PostRequest struct {
	// Reference is the cross-system key (NIP session reference); not unique.
	Reference string
	// Type is the transaction type (transfer | reversal | fee).
	Type canonical.Type
	// Status is the lifecycle state to record. Empty defaults to pending.
	Status canonical.Status
	// Entries are the journal lines; at least two, balanced, single-currency.
	Entries []EntryInput
	// Metadata is free-form context stored as JSONB.
	Metadata map[string]string
	// IdempotencyKey, when non-empty, dedupes the post: re-posting the same key
	// returns the existing transaction id instead of writing a duplicate.
	IdempotencyKey string
	// ParentTransactionID links a reversal to the transaction it compensates.
	// Nil for non-reversals.
	ParentTransactionID *uuid.UUID
}

// totals returns the summed debit and credit magnitudes across the entries.
func (r PostRequest) totals() (debits, credits money.Amount) {
	for _, e := range r.Entries {
		switch e.Direction {
		case Debit:
			debits = debits.Add(e.Amount)
		case Credit:
			credits = credits.Add(e.Amount)
		}
	}
	return debits, credits
}
