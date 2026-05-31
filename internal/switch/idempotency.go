package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

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

// ReserveResult is what Reserve returns. Response/Status are only meaningful for
// OutcomeReplay.
type ReserveResult struct {
	Outcome  Outcome
	Response []byte
	Status   string
}

// IdempotencyStore is the durable (Postgres) deduplication record. ADR-0003
// reserves a Redis fast-path in front of this; that is deferred — Postgres is
// the record of truth.
type IdempotencyStore struct {
	pool *pgxpool.Pool
	q    *switchdb.Queries
}

// IdempotencyStore implements Idempotency — checked at compile time.
var _ Idempotency = (*IdempotencyStore)(nil)

// NewIdempotencyStore builds a store over the given pool.
func NewIdempotencyStore(pool *pgxpool.Pool) *IdempotencyStore {
	return &IdempotencyStore{pool: pool, q: switchdb.New(pool)}
}

// Reserve atomically claims key for fingerprint. The INSERT ... ON CONFLICT DO
// NOTHING is the concurrency guard: exactly one caller inserts a row; everyone
// else falls through to inspect the existing record.
func (s *IdempotencyStore) Reserve(ctx context.Context, key, fingerprint string) (ReserveResult, error) {
	_, err := s.q.ReserveIdempotencyKey(ctx, switchdb.ReserveIdempotencyKeyParams{
		Key:                key,
		RequestFingerprint: fingerprint,
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
		return ReserveResult{Outcome: OutcomeReplay, Response: existing.Response, Status: existing.Status}, nil
	default: // in_progress
		return ReserveResult{Outcome: OutcomeInProgress}, nil
	}
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
