package transfer

import "testing"

func TestCanTransitionStatus(t *testing.T) {
	legal := []struct{ from, to string }{
		{statusPending, statusDebited},
		{statusPending, statusFailed},
		{statusDebited, statusSettled},
		{statusDebited, statusInDoubt},
		{statusDebited, statusReversalPending},
		{statusInDoubt, statusSettled},
		{statusInDoubt, statusReversalPending},
		{statusInDoubt, statusManualReview},
		{statusReversalPending, statusReversed},
	}
	for _, c := range legal {
		if !canTransitionStatus(c.from, c.to) {
			t.Errorf("expected %s -> %s to be legal", c.from, c.to)
		}
	}

	illegal := []struct{ from, to string }{
		{statusPending, statusSettled},         // skips debited
		{statusPending, statusReversalPending}, // cannot reverse before debiting
		{statusDebited, statusReversed},        // must pass through reversal_pending
		{statusSettled, statusReversed},        // terminal cannot move
		{statusReversed, statusSettled},        // terminal cannot move
		{statusReversalPending, statusSettled}, // a pending reversal only reverses
	}
	for _, c := range illegal {
		if canTransitionStatus(c.from, c.to) {
			t.Errorf("expected %s -> %s to be illegal", c.from, c.to)
		}
	}
}

func TestIsTerminalStatus(t *testing.T) {
	terminal := []string{statusSettled, statusReversed, statusFailed, statusManualReview}
	for _, s := range terminal {
		if !isTerminalStatus(s) {
			t.Errorf("expected %s to be terminal", s)
		}
	}
	nonTerminal := []string{statusPending, statusDebited, statusInDoubt, statusReversalPending}
	for _, s := range nonTerminal {
		if isTerminalStatus(s) {
			t.Errorf("expected %s to be non-terminal", s)
		}
	}
}

func TestStatusForState(t *testing.T) {
	cases := map[State]string{
		StateInitiated:          statusPending,
		StateDebitPending:       statusPending,
		StateDebited:            statusDebited,
		StateAwaitingSettlement: statusDebited,
		StateInDoubt:            statusInDoubt,
		StateReversalPending:    statusReversalPending,
		StateSettled:            statusSettled,
		StateReversed:           statusReversed,
		StateManualReview:       statusManualReview,
		StateFailed:             statusFailed,
	}
	for state, want := range cases {
		if got := statusForState(state); got != want {
			t.Errorf("statusForState(%s) = %q, want %q", state, got, want)
		}
	}
}

func TestStateForStatus(t *testing.T) {
	cases := map[string]State{
		statusPending:         StateDebitPending,
		statusDebited:         StateDebited,
		statusInDoubt:         StateInDoubt,
		statusReversalPending: StateReversalPending,
		statusSettled:         StateSettled,
		statusReversed:        StateReversed,
		statusManualReview:    StateManualReview,
		statusFailed:          StateFailed,
	}
	for status, want := range cases {
		if got := stateForStatus(status); got != want {
			t.Errorf("stateForStatus(%q) = %s, want %s", status, got, want)
		}
	}
}
