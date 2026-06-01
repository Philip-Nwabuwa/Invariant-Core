package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/metrics"
)

// serializationErr is a synthetic Postgres 40001 (the real one is only produced
// under live contention).
func serializationErr() error { return &pgconn.PgError{Code: serializationFailureCode} }

// TestRetry_ExhaustionSurfacesBackpressure: a write that aborts with 40001 on
// every attempt exhausts the budget and surfaces ErrSerializationExhausted
// (still a serialization failure underneath), counting retries + one exhaustion.
func TestRetry_ExhaustionSurfacesBackpressure(t *testing.T) {
	reg := metrics.New()
	m := NewMetrics(reg)

	const attempts = 3
	calls := 0
	err := retryOnSerialization(context.Background(), attempts, m, func() error {
		calls++
		return serializationErr()
	})

	if calls != attempts {
		t.Errorf("fn called %d times, want %d", calls, attempts)
	}
	if !errors.Is(err, ErrSerializationExhausted) {
		t.Errorf("err = %v, want ErrSerializationExhausted", err)
	}
	if !isSerializationFailure(err) {
		t.Errorf("underlying 40001 not preserved in %v", err)
	}
	// attempts-1 retries between the attempts, then one exhaustion.
	if got := testutil.ToFloat64(m.retries); got != attempts-1 {
		t.Errorf("retries counter = %v, want %d", got, attempts-1)
	}
	if got := testutil.ToFloat64(m.exhausted); got != 1 {
		t.Errorf("exhausted counter = %v, want 1", got)
	}
}

// TestRetry_SucceedsAfterTransientFailure: a 40001 that clears on retry succeeds
// without exhaustion, counting one retry.
func TestRetry_SucceedsAfterTransientFailure(t *testing.T) {
	reg := metrics.New()
	m := NewMetrics(reg)

	calls := 0
	err := retryOnSerialization(context.Background(), 5, m, func() error {
		calls++
		if calls == 1 {
			return serializationErr()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("err = %v, want nil after retry", err)
	}
	if got := testutil.ToFloat64(m.retries); got != 1 {
		t.Errorf("retries counter = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.exhausted); got != 0 {
		t.Errorf("exhausted counter = %v, want 0", got)
	}
}

// TestRetry_NonSerializationErrorPassesThrough: any other error returns
// immediately, unwrapped and uncounted.
func TestRetry_NonSerializationErrorPassesThrough(t *testing.T) {
	reg := metrics.New()
	m := NewMetrics(reg)

	sentinel := errors.New("boom")
	calls := 0
	err := retryOnSerialization(context.Background(), 5, m, func() error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want sentinel", err)
	}
	if errors.Is(err, ErrSerializationExhausted) {
		t.Error("non-40001 error must not be wrapped as exhaustion")
	}
	if calls != 1 {
		t.Errorf("fn called %d times, want 1 (no retry on non-40001)", calls)
	}
}

// TestPostErr_ExhaustionMapsToUnavailable: backpressure surfaces as Unavailable
// (transient/retryable), distinct from Internal — so the switch's outbox poller
// retries rather than failing the transfer closed.
func TestPostErr_ExhaustionMapsToUnavailable(t *testing.T) {
	err := postErrToStatus(errors.Join(ErrSerializationExhausted, serializationErr()))
	if status.Code(err) != codes.Unavailable {
		t.Errorf("status = %v, want Unavailable", status.Code(err))
	}
}
