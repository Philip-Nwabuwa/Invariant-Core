package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/ledgerdb"
)

// Repository is the ledger's data-access layer over the sqlc-generated queries.
// Read paths use the pool directly; write paths that must be atomic run through
// WithSerializableTx.
type Repository struct {
	pool    *pgxpool.Pool
	queries *ledgerdb.Queries
}

// NewRepository builds a Repository over the given pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, queries: ledgerdb.New(pool)}
}

// Queries returns the pool-scoped queries for non-transactional reads.
func (r *Repository) Queries() *ledgerdb.Queries { return r.queries }

// Pool exposes the underlying pool (used by tests that issue raw SQL).
func (r *Repository) Pool() *pgxpool.Pool { return r.pool }

// WithSerializableTx runs fn inside a single SERIALIZABLE transaction (ADR-0002).
// fn receives a transaction-scoped *ledgerdb.Queries; returning an error rolls
// the transaction back, otherwise it commits. Serialization failures (SQLSTATE
// 40001) surface to the caller, which is responsible for the bounded retry.
func (r *Repository) WithSerializableTx(ctx context.Context, fn func(q *ledgerdb.Queries) error) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin serializable tx: %w", err)
	}

	if err := fn(r.queries.WithTx(tx)); err != nil {
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
