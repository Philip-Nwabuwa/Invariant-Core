package transfer

import (
	"errors"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
)

// ErrIllegalTransition is returned when a transfer is asked to move between two
// states the machine does not allow.
var ErrIllegalTransition = errors.New("transfer: illegal state transition")

// transitions is the allowed forward edges of the happy-path machine
// (ARCHITECTURE §4). Sprint 3 adds the REVERSAL_PENDING / REVERSED branches.
var transitions = map[State][]State{
	StateInitiated:          {StateDebitPending},
	StateDebitPending:       {StateDebited},
	StateDebited:            {StateAwaitingSettlement},
	StateAwaitingSettlement: {StateSettled},
	StateSettled:            {}, // terminal
}

// CanTransition reports whether moving from s to "to" is a legal edge.
func (s State) CanTransition(to State) bool {
	for _, next := range transitions[s] {
		if next == to {
			return true
		}
	}
	return false
}

// statusForState maps a fine-grained machine State down to the coarse
// transactions.status value the DB enum allows. This is the locked NS-203
// decision: the rich state lives in Go, the coarse status is the single
// externalized source of truth (pending → debited → settled on the happy path).
func statusForState(s State) string {
	switch s {
	case StateDebited, StateAwaitingSettlement:
		return string(canonical.StatusDebited)
	case StateSettled:
		return string(canonical.StatusSettled)
	default: // StateInitiated, StateDebitPending
		return string(canonical.StatusPending)
	}
}

// stateForStatus maps a stored coarse status back to a representative State for
// the read model (GET). The MVP runs the happy path synchronously, so a stored
// transfer is normally already 'settled'.
func stateForStatus(status string) State {
	switch status {
	case string(canonical.StatusSettled):
		return StateSettled
	case string(canonical.StatusDebited):
		return StateDebited
	default:
		return StateInitiated
	}
}
