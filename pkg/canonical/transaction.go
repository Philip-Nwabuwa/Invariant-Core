// Package canonical defines the one transaction record every component shares.
//
// ADR-0005: both the ledger export and every settlement-file adapter normalize
// into this exact shape — that shared shape is what makes reconciliation matching
// possible at all. It lives in pkg/ (not internal/) precisely because it is the
// single type crossing every boundary. Adapters are the only place that knows
// about messy external formats; nothing downstream of an adapter sees a raw row.
package canonical

import (
	"time"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// Type is the kind of money movement a record represents.
type Type string

// Transaction types.
const (
	TypeTransfer Type = "transfer"
	TypeReversal Type = "reversal"
	TypeFee      Type = "fee"
)

// Status is the lifecycle state of a record at the time it was produced.
type Status string

// Lifecycle statuses.
const (
	StatusPending  Status = "pending"
	StatusDebited  Status = "debited"
	StatusSettled  Status = "settled"
	StatusFailed   Status = "failed"
	StatusTimedOut Status = "timed_out"
	StatusReversed Status = "reversed"
)

// Record is the canonical transaction record (ARCHITECTURE §3). Amounts are
// integer minor units via money.Amount; timestamps are UTC.
type Record struct {
	// TransactionID is the internal id. May be empty for external-only records.
	TransactionID string `json:"transaction_id,omitempty"`
	// Reference is the cross-system key (NIP end-to-end / session reference) and
	// the primary match key.
	Reference   string       `json:"reference"`
	Source      string       `json:"source"`
	Destination string       `json:"destination"`
	AmountMinor money.Amount `json:"amount_minor"`
	Currency    string       `json:"currency"`
	Type        Type         `json:"type"`
	Status      Status       `json:"status"`
	InitiatedAt time.Time    `json:"initiated_at"`
	// SettledAt is nil until the record has settled.
	SettledAt *time.Time `json:"settled_at,omitempty"`
	// Metadata is free-form and is never used as a match key.
	Metadata map[string]string `json:"metadata,omitempty"`
}
