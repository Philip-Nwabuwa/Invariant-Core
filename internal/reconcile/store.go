package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/reconcile/postgres/recondb"
)

// Store persists reconciliation runs and their exceptions to Postgres.
type Store struct {
	pool *pgxpool.Pool
	q    *recondb.Queries
}

// NewStore builds a store over the given pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: recondb.New(pool)}
}

// runSummary is the JSONB blob stored on recon_runs.summary. The fingerprint
// makes a run identifiable so an identical re-run is not double-counted (AC-4).
type runSummary struct {
	InputFingerprint string         `json:"input_fingerprint"`
	Matched          int            `json:"matched"`
	ByCategory       map[string]int `json:"by_category"`
}

// FindByFingerprint reports a previously-completed run for the same input
// fingerprint, if one exists. The second return value is false when none is
// found.
func (s *Store) FindByFingerprint(ctx context.Context, fingerprint string) (uuid.UUID, bool, error) {
	run, err := s.q.FindCompletedRunByFingerprint(ctx, fingerprint)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, fmt.Errorf("find run by fingerprint: %w", err)
	}
	return run.ID, true, nil
}

// Persist writes a run and all its exceptions in a single transaction. The run
// is opened as 'running', every exception is inserted, then the run is finished
// as 'completed' with counts + summary. Returns the new run id.
func (s *Store) Persist(ctx context.Context, internalSrc, externalSrc, fingerprint string, res Result) (uuid.UUID, error) {
	byCat := make(map[string]int)
	for _, e := range res.Exceptions {
		byCat[string(e.Category)]++
	}
	summary, err := json.Marshal(runSummary{
		InputFingerprint: fingerprint,
		Matched:          res.MatchedCount,
		ByCategory:       byCat,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("marshal summary: %w", err)
	}

	var runID uuid.UUID
	err = s.withTx(ctx, func(q *recondb.Queries) error {
		run, err := q.InsertReconRun(ctx, recondb.InsertReconRunParams{
			InternalSource: internalSrc,
			ExternalSource: externalSrc,
		})
		if err != nil {
			return fmt.Errorf("insert run: %w", err)
		}
		runID = run.ID

		for _, ex := range res.Exceptions {
			params, err := exceptionParams(run.ID, ex)
			if err != nil {
				return err
			}
			if _, err := q.InsertReconException(ctx, params); err != nil {
				return fmt.Errorf("insert exception: %w", err)
			}
		}

		if _, err := q.FinishReconRun(ctx, recondb.FinishReconRunParams{
			ID:             run.ID,
			MatchedCount:   int32(res.MatchedCount),
			ExceptionCount: int32(len(res.Exceptions)),
			Summary:        summary,
		}); err != nil {
			return fmt.Errorf("finish run: %w", err)
		}
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return runID, nil
}

// exceptionParams maps a domain Exception to sqlc insert params, marshaling the
// canonical records to JSONB.
func exceptionParams(runID uuid.UUID, ex Exception) (recondb.InsertReconExceptionParams, error) {
	p := recondb.InsertReconExceptionParams{
		RunID:      runID,
		Category:   string(ex.Category),
		DeltaMinor: ex.DeltaMinor,
	}
	if ex.Reference != "" {
		ref := ex.Reference
		p.Reference = &ref
	}
	if ex.Internal != nil {
		b, err := json.Marshal(ex.Internal)
		if err != nil {
			return p, fmt.Errorf("marshal internal record: %w", err)
		}
		p.InternalRecord = b
	}
	if ex.External != nil {
		b, err := json.Marshal(ex.External)
		if err != nil {
			return p, fmt.Errorf("marshal external record: %w", err)
		}
		p.ExternalRecord = b
	}
	return p, nil
}

// withTx runs fn inside a single transaction with a tx-scoped queries handle,
// mirroring internal/switch/store.go.
func (s *Store) withTx(ctx context.Context, fn func(q *recondb.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(s.q.WithTx(tx)); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			return errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
		}
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
