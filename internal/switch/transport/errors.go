package transport

import (
	"errors"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
)

// Stable, documented error codes (NS-506). These are part of the public API
// contract: clients branch on the code, not the human-readable message. They are
// mirrored in api/openapi/switch.yaml.
const (
	codeValidation     = "validation_error"
	codeMissingIdemKey = "missing_idempotency_key"
	codeIdemConflict   = "idempotency_conflict"
	codeInProgress     = "in_progress"
	codeNotFound       = "not_found"
	codeUnavailable    = "unavailable"
	codeInternal       = "internal"
)

// errInvalidJSONBody is the transport-level sentinel for a malformed request
// body, so it classifies as a validation error rather than an internal one.
var errInvalidJSONBody = errors.New("transfer: invalid JSON body")

// apiError pairs a stable code with the HTTP status it surfaces as.
type apiError struct {
	code   string
	status int
}

// classify maps a domain (or transport) error to its public code + HTTP status.
// Unknown errors are an opaque 500 so internal detail never leaks to the client.
func classify(err error) apiError {
	switch {
	case errors.Is(err, transfer.ErrMissingIdempotencyKey):
		return apiError{codeMissingIdemKey, http.StatusBadRequest}
	case errors.Is(err, transfer.ErrIdempotencyConflict):
		return apiError{codeIdemConflict, http.StatusConflict}
	case errors.Is(err, transfer.ErrInProgress):
		return apiError{codeInProgress, http.StatusConflict}
	case errors.Is(err, transfer.ErrNotFound):
		return apiError{codeNotFound, http.StatusNotFound}
	case errors.Is(err, errInvalidJSONBody),
		errors.Is(err, transfer.ErrNonPositiveAmount),
		errors.Is(err, transfer.ErrUnknownCurrency),
		errors.Is(err, transfer.ErrMissingField):
		return apiError{codeValidation, http.StatusBadRequest}
	case isUnavailable(err):
		// Ledger serialization-retry backpressure (NS-505) propagated as gRPC
		// Unavailable: a transient 503 the client may safely retry.
		return apiError{codeUnavailable, http.StatusServiceUnavailable}
	default:
		return apiError{codeInternal, http.StatusInternalServerError}
	}
}

// isUnavailable reports whether err is (or wraps) a gRPC Unavailable status — the
// backpressure signal the ledger raises when the serialization-retry budget is
// exhausted. It unwraps, since the engine wraps the gRPC error with context.
func isUnavailable(err error) bool {
	var se interface{ GRPCStatus() *status.Status }
	if errors.As(err, &se) {
		return se.GRPCStatus().Code() == codes.Unavailable
	}
	return false
}
