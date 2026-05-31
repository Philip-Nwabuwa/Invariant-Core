package ledger

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// serializationFailureCode is the SQLSTATE Postgres returns when it aborts a
// transaction to preserve serializability (ADR-0002).
const serializationFailureCode = "40001"

// uniqueViolationCode is the SQLSTATE for a unique-constraint violation. It is
// the opposite of a serialization failure: not transient, but a definitive
// "already exists" — used to make idempotency-key re-posts a no-op.
const uniqueViolationCode = "23505"

const (
	retryBaseBackoff = 5 * time.Millisecond
	retryMaxBackoff  = 200 * time.Millisecond
)

// retryOnSerialization runs fn, retrying up to attempts times while fn fails
// with a serialization failure (40001), backing off exponentially between
// tries. Any other error returns immediately. The context bounds the waits.
func retryOnSerialization(ctx context.Context, attempts int, fn func() error) error {
	backoff := retryBaseBackoff
	var err error
	for i := 0; i < attempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
		if !isSerializationFailure(err) {
			return err
		}
		if i == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > retryMaxBackoff {
			backoff = retryMaxBackoff
		}
	}
	return err
}

// isSerializationFailure reports whether err is (or wraps) a Postgres 40001.
func isSerializationFailure(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == serializationFailureCode
	}
	return false
}

// isUniqueViolation reports whether err is (or wraps) a Postgres 23505.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == uniqueViolationCode
	}
	return false
}
