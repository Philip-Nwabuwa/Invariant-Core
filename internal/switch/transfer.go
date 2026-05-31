// Package transfer is the switch's transfer engine: it accepts a money transfer,
// drives it through the state machine (ARCHITECTURE §4), and moves money through
// the rail and the ledger exactly once.
//
// The package lives in internal/switch, but its identifier is "transfer" because
// "switch" is a reserved Go keyword and cannot name a package.
package transfer

import (
	"context"
	"errors"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// State is a transfer's position in the ARCHITECTURE §4 state machine. The full
// transition table is defined in statemachine.go (NS-203); Sprint 3 adds the
// REVERSAL_PENDING and REVERSED states.
type State string

const (
	// StateInitiated is the entry state before any side effect (transient; never
	// persisted — the row is created directly at DEBIT_PENDING).
	StateInitiated State = "INITIATED"
	// StateDebitPending is after the idempotency key is reserved and validated.
	StateDebitPending State = "DEBIT_PENDING"
	// StateDebited is after the ledger debits the source.
	StateDebited State = "DEBITED"
	// StateAwaitingSettlement is after the rail call is sent.
	StateAwaitingSettlement State = "AWAITING_SETTLEMENT"
	// StateSettled is the happy-path terminal state.
	StateSettled State = "SETTLED"
	// StateInDoubt is reached when the rail outcome is unknown (timeout). It is
	// resolved by a TSQ before any reversal — never reverse an unconfirmed item.
	StateInDoubt State = "IN_DOUBT"
	// StateReversalPending is reached on a rail decline or a TSQ-confirmed
	// no-settlement; a compensating reversal is enqueued to restore the source.
	StateReversalPending State = "REVERSAL_PENDING"
	// StateReversed is the terminal state after the compensating entries post.
	StateReversed State = "REVERSED"
	// StateManualReview holds a transfer whose outcome could not be determined
	// (e.g. the TSQ itself kept timing out) for an operator to resolve.
	StateManualReview State = "MANUAL_REVIEW"
	// StateFailed is terminal: the transfer was rejected before any money moved.
	StateFailed State = "FAILED"
)

// Supported currencies (NGN-only in v1, matching pkg/money).
const currencyNGN = "NGN"

// Validation / lookup errors. The transport layer maps these to HTTP status
// codes; using sentinel errors keeps that mapping in one place (errors.Is).
var (
	// ErrMissingIdempotencyKey signals the required Idempotency-Key header was absent.
	ErrMissingIdempotencyKey = errors.New("transfer: idempotency-key header required")
	// ErrNonPositiveAmount signals amount_minor <= 0.
	ErrNonPositiveAmount = errors.New("transfer: amount_minor must be positive")
	// ErrUnknownCurrency signals an unsupported currency code.
	ErrUnknownCurrency = errors.New("transfer: unsupported currency")
	// ErrMissingField signals a required body field was empty.
	ErrMissingField = errors.New("transfer: required field missing")
	// ErrNotFound signals no transfer exists for the given id.
	ErrNotFound = errors.New("transfer: not found")
	// ErrIdempotencyConflict signals the same Idempotency-Key was reused with a
	// different request body — maps to HTTP 409.
	ErrIdempotencyConflict = errors.New("transfer: idempotency-key reused with a different request")
	// ErrInProgress signals another request with this Idempotency-Key is still
	// being processed — maps to HTTP 409.
	ErrInProgress = errors.New("transfer: a request with this idempotency-key is in progress")
)

// CreateRequest is the validated domain input for a new transfer.
type CreateRequest struct {
	Source      string
	Destination string
	Amount      money.Amount
	Currency    string
	Reference   string
}

// Validate enforces the field-level invariants the engine relies on. It returns
// one of the sentinel errors above so the transport layer can map it to a 400.
func (r CreateRequest) Validate() error {
	if r.Source == "" || r.Destination == "" || r.Reference == "" {
		return ErrMissingField
	}
	if r.Currency != currencyNGN {
		return ErrUnknownCurrency
	}
	if r.Amount.Minor() <= 0 {
		return ErrNonPositiveAmount
	}
	return nil
}

// View is the read model returned to API callers.
type View struct {
	ID          string
	Reference   string
	Source      string
	Destination string
	Amount      money.Amount
	Currency    string
	State       State
}

// Service is the behaviour the transport layer depends on. NS-201 wires a stub
// behind this interface; NS-203/205 swap in the real orchestrator without the
// transport layer changing — that is the point of depending on the interface
// rather than a concrete type.
type Service interface {
	// Create processes a new transfer. idempotencyKey is the caller-supplied
	// Idempotency-Key header (deduplication lands in NS-202).
	Create(ctx context.Context, idempotencyKey string, req CreateRequest) (View, error)
	// Get returns the current view of a transfer by id, or ErrNotFound.
	Get(ctx context.Context, id string) (View, error)
}
