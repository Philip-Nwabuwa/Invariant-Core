package transfer

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestIsTerminalLedgerError_BackpressureIsTransient: a ledger Unavailable (the
// serialization-retry-exhausted backpressure from NS-505) is NOT terminal, so
// the driver returns it and the outbox poller retries — the transfer is never
// failed closed. NotFound/InvalidArgument remain terminal (a re-drive cannot fix
// an unknown account or an invalid request).
func TestIsTerminalLedgerError_BackpressureIsTransient(t *testing.T) {
	cases := []struct {
		code codes.Code
		want bool
	}{
		{codes.Unavailable, false},
		{codes.Internal, false},
		{codes.NotFound, true},
		{codes.InvalidArgument, true},
	}
	for _, c := range cases {
		err := status.Error(c.code, "ledger says so")
		if got := isTerminalLedgerError(err); got != c.want {
			t.Errorf("isTerminalLedgerError(%v) = %t, want %t", c.code, got, c.want)
		}
	}
}
