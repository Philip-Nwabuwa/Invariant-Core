package transfer

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
)

// Idempotency is the deduplication behaviour the decorator depends on.
// *IdempotencyStore satisfies it; tests substitute a fake. Defining the
// interface here (not where the store lives) lets the decorator depend on
// behaviour rather than the concrete Postgres type.
type Idempotency interface {
	// Reserve atomically claims key for the request fingerprint and reports what
	// it found (new, replay, conflict, or in-progress).
	Reserve(ctx context.Context, key, fingerprint string) (ReserveResult, error)
	// Complete records the terminal outcome for a previously reserved key.
	Complete(ctx context.Context, key, status string, txID *uuid.UUID, response []byte) error
}

// IdempotentService wraps a Service with durable deduplication. It reserves the
// Idempotency-Key before any work runs, so a duplicate request short-circuits
// here and the inner service never creates a second transaction (DoD: a
// duplicate key is a no-op). On success it records the response for verbatim
// replay; on failure it marks the key failed so a retry can proceed.
type IdempotentService struct {
	inner Service
	idem  Idempotency
}

// IdempotentService implements Service — checked at compile time.
var _ Service = (*IdempotentService)(nil)

// NewIdempotentService composes inner behind the idempotency store.
func NewIdempotentService(inner Service, idem Idempotency) *IdempotentService {
	return &IdempotentService{inner: inner, idem: idem}
}

// Create deduplicates on key, then delegates to the inner service.
func (s *IdempotentService) Create(ctx context.Context, key string, req CreateRequest) (View, error) {
	if err := req.Validate(); err != nil {
		return View{}, err
	}

	res, err := s.idem.Reserve(ctx, key, Fingerprint(req))
	if err != nil {
		return View{}, err
	}

	switch res.Outcome {
	case OutcomeConflict:
		return View{}, ErrIdempotencyConflict
	case OutcomeInProgress:
		return View{}, ErrInProgress
	case OutcomeReplay:
		var view View
		if err := json.Unmarshal(res.Response, &view); err != nil {
			return View{}, err
		}
		return view, nil
	}

	// OutcomeReserved: we own the key. Run the transfer, then record the result.
	view, err := s.inner.Create(ctx, key, req)
	if err != nil {
		// Best-effort: release the key as failed so a retry isn't blocked. The
		// original error is what the caller cares about.
		_ = s.idem.Complete(ctx, key, IdemFailed, nil, nil)
		return View{}, err
	}

	response, err := json.Marshal(view)
	if err != nil {
		return View{}, err
	}
	var txID *uuid.UUID
	if id, perr := uuid.Parse(view.ID); perr == nil {
		txID = &id
	}
	if err := s.idem.Complete(ctx, key, IdemSucceeded, txID, response); err != nil {
		return View{}, err
	}
	return view, nil
}

// Get delegates unchanged; reads are naturally idempotent.
func (s *IdempotentService) Get(ctx context.Context, id string) (View, error) {
	return s.inner.Get(ctx, id)
}
