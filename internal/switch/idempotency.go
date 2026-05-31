package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/switch/postgres/switchdb"
)

// idempotency_keys.status values (CHECK constraint in db/schema.sql).
const (
	IdemInProgress = "in_progress"
	IdemSucceeded  = "succeeded"
	IdemFailed     = "failed"
)

// Outcome classifies what Reserve found for a key.
type Outcome int

const (
	// OutcomeReserved means the key was brand-new and is now claimed; the caller
	// should process the transfer and then call Complete.
	OutcomeReserved Outcome = iota
	// OutcomeReplay means the key already completed; Response holds the stored
	// result to return verbatim, and no work should be redone.
	OutcomeReplay
	// OutcomeConflict means the key exists with a different request fingerprint
	// (a key reused for a different body) — maps to HTTP 409.
	OutcomeConflict
	// OutcomeInProgress means another request holds the key and hasn't completed.
	OutcomeInProgress
)

// ReserveResult is what Reserve returns. Response/Status/TransactionID are only
// meaningful for OutcomeReplay (and TransactionID for in-progress takeover).
type ReserveResult struct {
	Outcome       Outcome
	Response      []byte
	Status        string
	TransactionID *uuid.UUID
}

// defaultIdempotencyLease bounds how long an in-progress key blocks a replay
// before it is treated as a crashed holder and taken over (DESIGN-NOTES §5).
const defaultIdempotencyLease = 30 * time.Second

// IdempotencyStore is the durable (Postgres) deduplication record. ADR-0003
// reserves a Redis fast-path in front of this; that is deferred — Postgres is
// the record of truth.
type IdempotencyStore struct {
	pool  *pgxpool.Pool
	q     *switchdb.Queries
	lease time.Duration
}

// IdempotencyStore implements Idempotency — checked at compile time.
var _ Idempotency = (*IdempotencyStore)(nil)

// NewIdempotencyStore builds a store over the given pool.
func NewIdempotencyStore(pool *pgxpool.Pool) *IdempotencyStore {
	return &IdempotencyStore{pool: pool, q: switchdb.New(pool), lease: defaultIdempotencyLease}
}

// WithLease overrides the in-progress lease (used by tests).
func (s *IdempotencyStore) WithLease(d time.Duration) *IdempotencyStore {
	s.lease = d
	return s
}

// Reserve atomically claims key for fingerprint. The INSERT ... ON CONFLICT DO
// NOTHING is the concurrency guard: exactly one caller inserts a row; everyone
// else falls through to inspect the existing record.
func (s *IdempotencyStore) Reserve(ctx context.Context, key, fingerprint string) (ReserveResult, error) {
	_, err := s.q.ReserveIdempotencyKey(ctx, switchdb.ReserveIdempotencyKeyParams{
		Key:                key,
		RequestFingerprint: fingerprint,
		LeaseSeconds:       s.lease.Seconds(),
	})
	if err == nil {
		return ReserveResult{Outcome: OutcomeReserved}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ReserveResult{}, fmt.Errorf("reserve idempotency key: %w", err)
	}

	// ON CONFLICT returned no row: the key already exists. Inspect it.
	existing, gerr := s.q.GetIdempotencyKey(ctx, key)
	if gerr != nil {
		return ReserveResult{}, fmt.Errorf("get idempotency key: %w", gerr)
	}
	if existing.RequestFingerprint != fingerprint {
		return ReserveResult{Outcome: OutcomeConflict}, nil
	}
	switch existing.Status {
	case IdemSucceeded, IdemFailed:
		return ReserveResult{
			Outcome:       OutcomeReplay,
			Response:      existing.Response,
			Status:        existing.Status,
			TransactionID: existing.TransactionID,
		}, nil
	default: // in_progress
		return s.inProgressResult(ctx, key, existing)
	}
}

// inProgressResult decides what a replay of an in-progress key sees. Within the
// lease the original may still be completing, so it is genuinely in progress.
// Past the lease the original holder is presumed crashed: re-attach to the
// transfer it created (the customer key is bound on the transfer row even if the
// idempotency record was never completed) and replay its live state. The poller
// then drives that transfer to terminal (DESIGN-NOTES §5).
func (s *IdempotencyStore) inProgressResult(ctx context.Context, key string, existing switchdb.IdempotencyKey) (ReserveResult, error) {
	leaseLive := existing.ExpiresAt != nil && existing.ExpiresAt.After(time.Now())
	if leaseLive {
		return ReserveResult{Outcome: OutcomeInProgress, TransactionID: existing.TransactionID}, nil
	}

	txID := existing.TransactionID
	if txID == nil {
		// The idempotency record was never linked, but the transfer row carries
		// the customer key — resolve it there.
		k := key
		if id, lerr := s.q.GetTransferIDByIdempotencyKey(ctx, &k); lerr == nil {
			txID = &id
		}
	}
	if txID != nil {
		return ReserveResult{Outcome: OutcomeReplay, Status: IdemInProgress, TransactionID: txID}, nil
	}
	// No transfer was ever created: still treat as in-progress (the caller retries).
	return ReserveResult{Outcome: OutcomeInProgress}, nil
}

// Complete records the terminal outcome for a previously reserved key. txID may
// be nil when no transaction was created (e.g. a failure before posting).
func (s *IdempotencyStore) Complete(ctx context.Context, key, status string, txID *uuid.UUID, response []byte) error {
	if err := s.q.CompleteIdempotencyKey(ctx, switchdb.CompleteIdempotencyKeyParams{
		Key:           key,
		Status:        status,
		Response:      response,
		TransactionID: txID,
	}); err != nil {
		return fmt.Errorf("complete idempotency key: %w", err)
	}
	return nil
}

// Fingerprint returns a stable SHA-256 hex digest of the canonical form of req.
// It is the basis for detecting a reused Idempotency-Key that carries a
// different body. json.Marshal of a struct is deterministic (fields encode in
// declaration order), so the digest is stable for equal requests.
func Fingerprint(req CreateRequest) string {
	b, _ := json.Marshal(struct {
		Source      string `json:"source"`
		Destination string `json:"destination"`
		AmountMinor int64  `json:"amount_minor"`
		Currency    string `json:"currency"`
		Reference   string `json:"reference"`
	}{
		Source:      req.Source,
		Destination: req.Destination,
		AmountMinor: req.Amount.Minor(),
		Currency:    req.Currency,
		Reference:   req.Reference,
	})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
