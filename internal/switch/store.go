package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/switch/postgres/switchdb"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// transferMeta is the JSON stored in transactions.metadata so the read model can
// reconstruct fields that live in ledger entries, not on the transfer row.
type transferMeta struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	AmountMinor int64  `json:"amount_minor"`
}

// PostgresStore persists a transfer's lifecycle row (the single externalized
// source of state). It is the concrete Store used by the orchestrator.
type PostgresStore struct {
	pool *pgxpool.Pool
	q    *switchdb.Queries
}

// PostgresStore implements Store — verified at compile time.
var _ Store = (*PostgresStore)(nil)

// NewPostgresStore builds a store over the given pool.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool, q: switchdb.New(pool)}
}

// Queries exposes the pool-scoped queries for non-transactional reads.
func (s *PostgresStore) Queries() *switchdb.Queries { return s.q }

// WithTx runs fn inside a single DB transaction, passing it a tx-scoped Queries.
// It is how a state change and the outbox event that must follow it commit
// together (no dual-write). Returning an error rolls back; otherwise it commits.
func (s *PostgresStore) WithTx(ctx context.Context, fn func(q *switchdb.Queries) error) error {
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

// Create inserts a new transfer row at status 'pending' and returns its id.
func (s *PostgresStore) Create(ctx context.Context, key string, req CreateRequest) (uuid.UUID, error) {
	meta, err := json.Marshal(transferMeta{
		Source:      req.Source,
		Destination: req.Destination,
		AmountMinor: req.Amount.Minor(),
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("marshal metadata: %w", err)
	}
	k := key
	tx, err := s.q.InsertTransfer(ctx, switchdb.InsertTransferParams{
		Reference:      req.Reference,
		Status:         string(canonical.StatusPending),
		IdempotencyKey: &k,
		Currency:       req.Currency,
		Metadata:       meta,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("insert transfer: %w", err)
	}
	return tx.ID, nil
}

// SetStatus updates the coarse transactions.status.
func (s *PostgresStore) SetStatus(ctx context.Context, id uuid.UUID, status string) error {
	if err := s.q.SetTransferStatus(ctx, switchdb.SetTransferStatusParams{ID: id, Status: status}); err != nil {
		return fmt.Errorf("set transfer status: %w", err)
	}
	return nil
}

// SetSettled marks the transfer settled and stamps settled_at.
func (s *PostgresStore) SetSettled(ctx context.Context, id uuid.UUID) error {
	if err := s.q.SetTransferSettled(ctx, id); err != nil {
		return fmt.Errorf("set transfer settled: %w", err)
	}
	return nil
}

// Get returns the read-model View for a transfer, or ErrNotFound.
func (s *PostgresStore) Get(ctx context.Context, id uuid.UUID) (View, error) {
	tx, err := s.q.GetTransferByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return View{}, ErrNotFound
		}
		return View{}, fmt.Errorf("get transfer: %w", err)
	}
	var meta transferMeta
	if len(tx.Metadata) > 0 {
		_ = json.Unmarshal(tx.Metadata, &meta)
	}
	return View{
		ID:          tx.ID.String(),
		Reference:   tx.Reference,
		Source:      meta.Source,
		Destination: meta.Destination,
		Amount:      money.FromMinor(meta.AmountMinor),
		Currency:    tx.Currency,
		State:       stateForStatus(tx.Status),
	}, nil
}
