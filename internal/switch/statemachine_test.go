package transfer

import (
	"testing"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
)

func TestCanTransition(t *testing.T) {
	legal := []struct{ from, to State }{
		{StateInitiated, StateDebitPending},
		{StateDebitPending, StateDebited},
		{StateDebited, StateAwaitingSettlement},
		{StateAwaitingSettlement, StateSettled},
	}
	for _, c := range legal {
		if !c.from.CanTransition(c.to) {
			t.Errorf("expected %s -> %s to be legal", c.from, c.to)
		}
	}

	illegal := []struct{ from, to State }{
		{StateInitiated, StateSettled},          // skips the middle
		{StateInitiated, StateDebited},          // skips DEBIT_PENDING
		{StateDebited, StateSettled},            // skips AWAITING_SETTLEMENT
		{StateSettled, StateInitiated},          // terminal cannot move
		{StateAwaitingSettlement, StateDebited}, // no going backwards
	}
	for _, c := range illegal {
		if c.from.CanTransition(c.to) {
			t.Errorf("expected %s -> %s to be illegal", c.from, c.to)
		}
	}
}

func TestStatusForState(t *testing.T) {
	cases := map[State]string{
		StateInitiated:          string(canonical.StatusPending),
		StateDebitPending:       string(canonical.StatusPending),
		StateDebited:            string(canonical.StatusDebited),
		StateAwaitingSettlement: string(canonical.StatusDebited),
		StateSettled:            string(canonical.StatusSettled),
	}
	for state, want := range cases {
		if got := statusForState(state); got != want {
			t.Errorf("statusForState(%s) = %q, want %q", state, got, want)
		}
	}
}

func TestStateForStatus(t *testing.T) {
	cases := map[string]State{
		string(canonical.StatusPending): StateInitiated,
		string(canonical.StatusDebited): StateDebited,
		string(canonical.StatusSettled): StateSettled,
	}
	for status, want := range cases {
		if got := stateForStatus(status); got != want {
			t.Errorf("stateForStatus(%q) = %s, want %s", status, got, want)
		}
	}
}
