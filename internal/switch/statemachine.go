package transfer

import (
	"errors"
)

// ErrIllegalTransition is returned when a transfer is asked to move between two
// states the machine does not allow.
var ErrIllegalTransition = errors.New("transfer: illegal state transition")

// Coarse transactions.status values — the single externalized source of truth
// (migration 0002 grows the CHECK with the in-doubt / reversal / manual-review
// states). The driver works directly with these; the rich State type below is a
// read-model projection. They are distinct (no overload) so a crashed transfer
// resumes at exactly the step it had reached.
const (
	statusPending         = "pending"          // INITIATED / DEBIT_PENDING
	statusDebited         = "debited"          // DEBITED / AWAITING_SETTLEMENT
	statusInDoubt         = "in_doubt"         // IN_DOUBT (awaiting a TSQ)
	statusReversalPending = "reversal_pending" // REVERSAL_PENDING
	statusSettled         = "settled"          // terminal
	statusReversed        = "reversed"         // terminal
	statusManualReview    = "manual_review"    // held terminal
	statusFailed          = "failed"           // terminal
)

// statusTransitions is the legal forward edges between coarse statuses. The
// driver consults it (under a row lock) before advancing, so an out-of-order or
// duplicate event is an idempotent no-op rather than a corruption.
var statusTransitions = map[string][]string{
	statusPending:         {statusDebited, statusFailed},
	statusDebited:         {statusSettled, statusInDoubt, statusReversalPending},
	statusInDoubt:         {statusSettled, statusReversalPending, statusManualReview},
	statusReversalPending: {statusReversed},
	// settled / reversed / failed / manual_review are terminal.
}

// canTransitionStatus reports whether moving from -> to is a legal coarse edge.
func canTransitionStatus(from, to string) bool {
	for _, next := range statusTransitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

// isTerminalStatus reports whether a status has no outgoing edges.
func isTerminalStatus(status string) bool {
	return len(statusTransitions[status]) == 0
}

// CanTransition reports whether moving from State s to "to" is a legal edge. It
// is retained for the white-box state-machine tests; the driver guards on coarse
// status (canTransitionStatus) since that is what is persisted.
func (s State) CanTransition(to State) bool {
	return canTransitionStatus(statusForState(s), statusForState(to))
}

// statusForState maps a rich machine State down to the coarse transactions.status.
func statusForState(s State) string {
	switch s {
	case StateDebited, StateAwaitingSettlement:
		return statusDebited
	case StateInDoubt:
		return statusInDoubt
	case StateReversalPending:
		return statusReversalPending
	case StateSettled:
		return statusSettled
	case StateReversed:
		return statusReversed
	case StateManualReview:
		return statusManualReview
	case StateFailed:
		return statusFailed
	default: // StateInitiated, StateDebitPending
		return statusPending
	}
}

// stateForStatus maps a stored coarse status back to a representative State for
// the read model (GET). With distinct statuses the mapping is 1:1, so a transfer
// is reconstructed at its true position.
func stateForStatus(status string) State {
	switch status {
	case statusDebited:
		return StateDebited
	case statusInDoubt:
		return StateInDoubt
	case statusReversalPending:
		return StateReversalPending
	case statusSettled:
		return StateSettled
	case statusReversed:
		return StateReversed
	case statusManualReview:
		return StateManualReview
	case statusFailed:
		return StateFailed
	default: // pending
		return StateDebitPending
	}
}
