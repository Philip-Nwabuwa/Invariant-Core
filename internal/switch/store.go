package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/switch/outbox"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/switch/postgres/switchdb"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// transferMeta is the JSON stored in transactions.metadata so the read model can
// reconstruct fields that live in ledger entries, not on the transfer row, plus
// the debit leg's ledger transaction id (the reversal parent).
type transferMeta struct {
	Source       string     `json:"source"`
	Destination  string     `json:"destination"`
	AmountMinor  int64      `json:"amount_minor"`
	DebitLegTxID *uuid.UUID `json:"debit_leg_tx_id,omitempty"`
}

// transferDetail is the driver's view of a persisted transfer: its lifecycle
// row plus the fields parsed out of metadata.
type transferDetail struct {
	ID           uuid.UUID
	Reference    string
	Currency     string
	Status       string
	Source       string
	Destination  string
	Amount       money.Amount
	DebitLegTxID *uuid.UUID
}

// transfer rebuilds the in-flight Transfer the ledger and rail clients consume.
func (d transferDetail) transfer() Transfer {
	return Transfer{
		ID:          d.ID,
		Reference:   d.Reference,
		Source:      d.Source,
		Destination: d.Destination,
		Amount:      d.Amount,
		Currency:    d.Currency,
	}
}

// PostgresStore persists a transfer's lifecycle row (the single externalized
// source of state) and the outbox events that drive it forward.
type PostgresStore struct {
	pool *pgxpool.Pool
	q    *switchdb.Queries
}

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

// CreatePending inserts a transfer at status 'pending' (DEBIT_PENDING) and
// appends the transfer.debit_requested outbox event in the SAME transaction.
// Persisting the intent before any cross-service write is what closes the
// stranded-debit window: if the process dies before the debit posts, the poller
// still finds the durable event and reconciles against the ledger by reference.
func (s *PostgresStore) CreatePending(ctx context.Context, key string, req CreateRequest) (uuid.UUID, error) {
	meta, err := json.Marshal(transferMeta{
		Source:      req.Source,
		Destination: req.Destination,
		AmountMinor: req.Amount.Minor(),
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("marshal metadata: %w", err)
	}

	var id uuid.UUID
	err = s.WithTx(ctx, func(q *switchdb.Queries) error {
		k := key
		tx, err := q.InsertTransfer(ctx, switchdb.InsertTransferParams{
			Reference:      req.Reference,
			Status:         statusPending,
			IdempotencyKey: &k,
			Currency:       req.Currency,
			Metadata:       meta,
		})
		if err != nil {
			return fmt.Errorf("insert transfer: %w", err)
		}
		id = tx.ID
		payload, err := json.Marshal(transferEventPayload{
			Reference:   req.Reference,
			Source:      req.Source,
			Destination: req.Destination,
			AmountMinor: req.Amount.Minor(),
			Currency:    req.Currency,
		})
		if err != nil {
			return fmt.Errorf("marshal event: %w", err)
		}
		return outbox.Append(ctx, q, id, outbox.EventDebitRequested, payload)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// load returns the full driver view of a transfer, or ErrNotFound.
func (s *PostgresStore) load(ctx context.Context, id uuid.UUID) (transferDetail, error) {
	tx, err := s.q.GetTransferByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return transferDetail{}, ErrNotFound
		}
		return transferDetail{}, fmt.Errorf("get transfer: %w", err)
	}
	return detailFromRow(tx), nil
}

func detailFromRow(tx switchdb.Transaction) transferDetail {
	var meta transferMeta
	if len(tx.Metadata) > 0 {
		_ = json.Unmarshal(tx.Metadata, &meta)
	}
	return transferDetail{
		ID:           tx.ID,
		Reference:    tx.Reference,
		Currency:     tx.Currency,
		Status:       tx.Status,
		Source:       meta.Source,
		Destination:  meta.Destination,
		Amount:       money.FromMinor(meta.AmountMinor),
		DebitLegTxID: meta.DebitLegTxID,
	}
}

// advance locks the transfer row, and if current->to is a legal coarse edge,
// runs apply (the status change plus any outbox events) atomically. It reports
// whether it advanced: false means an idempotent no-op because the transfer had
// already moved past this edge (at-least-once delivery, a duplicate callback, or
// a re-driven recovery). The row lock serializes concurrent advances.
func (s *PostgresStore) advance(ctx context.Context, id uuid.UUID, to string, apply func(q *switchdb.Queries, d transferDetail) error) (bool, error) {
	advanced := false
	err := s.WithTx(ctx, func(q *switchdb.Queries) error {
		row, err := q.GetTransferForUpdate(ctx, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock transfer: %w", err)
		}
		if !canTransitionStatus(row.Status, to) {
			return nil // idempotent no-op
		}
		if err := apply(q, detailFromRow(row)); err != nil {
			return err
		}
		advanced = true
		return nil
	})
	return advanced, err
}

// markDebited advances pending -> debited, records the debit leg's ledger tx id,
// and enqueues transfer.debited.
func (s *PostgresStore) markDebited(ctx context.Context, id, debitLegTxID uuid.UUID, payload []byte) (bool, error) {
	return s.advance(ctx, id, statusDebited, func(q *switchdb.Queries, _ transferDetail) error {
		if err := q.SetTransferStatusAndDebitLeg(ctx, switchdb.SetTransferStatusAndDebitLegParams{
			ID:           id,
			Status:       statusDebited,
			DebitLegTxID: debitLegTxID.String(),
		}); err != nil {
			return fmt.Errorf("set debited: %w", err)
		}
		return outbox.Append(ctx, q, id, outbox.EventDebited, payload)
	})
}

// markSettled advances debited/in_doubt -> settled (terminal).
func (s *PostgresStore) markSettled(ctx context.Context, id uuid.UUID) (bool, error) {
	return s.advance(ctx, id, statusSettled, func(q *switchdb.Queries, _ transferDetail) error {
		if err := q.SetTransferSettled(ctx, id); err != nil {
			return fmt.Errorf("set settled: %w", err)
		}
		return nil
	})
}

// markInDoubt advances debited -> in_doubt and enqueues transfer.in_doubt.
func (s *PostgresStore) markInDoubt(ctx context.Context, id uuid.UUID, payload []byte) (bool, error) {
	return s.advance(ctx, id, statusInDoubt, func(q *switchdb.Queries, _ transferDetail) error {
		if err := q.SetTransferStatus(ctx, switchdb.SetTransferStatusParams{ID: id, Status: statusInDoubt}); err != nil {
			return fmt.Errorf("set in_doubt: %w", err)
		}
		return outbox.Append(ctx, q, id, outbox.EventInDoubt, payload)
	})
}

// markReversalPending advances debited/in_doubt -> reversal_pending and enqueues
// reversal.requested.
func (s *PostgresStore) markReversalPending(ctx context.Context, id uuid.UUID, payload []byte) (bool, error) {
	return s.advance(ctx, id, statusReversalPending, func(q *switchdb.Queries, _ transferDetail) error {
		if err := q.SetTransferStatus(ctx, switchdb.SetTransferStatusParams{ID: id, Status: statusReversalPending}); err != nil {
			return fmt.Errorf("set reversal_pending: %w", err)
		}
		return outbox.Append(ctx, q, id, outbox.EventReversalRequested, payload)
	})
}

// markFailed advances pending -> failed (terminal; no money moved).
func (s *PostgresStore) markFailed(ctx context.Context, id uuid.UUID) (bool, error) {
	return s.advance(ctx, id, statusFailed, func(q *switchdb.Queries, _ transferDetail) error {
		if err := q.SetTransferStatus(ctx, switchdb.SetTransferStatusParams{ID: id, Status: statusFailed}); err != nil {
			return fmt.Errorf("set failed: %w", err)
		}
		return nil
	})
}

// Get returns the read-model View for a transfer, or ErrNotFound.
func (s *PostgresStore) Get(ctx context.Context, id uuid.UUID) (View, error) {
	d, err := s.load(ctx, id)
	if err != nil {
		return View{}, err
	}
	return View{
		ID:          d.ID.String(),
		Reference:   d.Reference,
		Source:      d.Source,
		Destination: d.Destination,
		Amount:      d.Amount,
		Currency:    d.Currency,
		State:       stateForStatus(d.Status),
	}, nil
}
