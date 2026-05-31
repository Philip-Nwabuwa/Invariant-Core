package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/ledgerdb"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// GetAccount returns an account by its code.
func (s *Service) GetAccount(ctx context.Context, code string) (ledgerdb.Account, error) {
	acct, err := s.repo.Queries().GetAccountByCode(ctx, code)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ledgerdb.Account{}, fmt.Errorf("%w: %s", ErrUnknownAccount, code)
		}
		return ledgerdb.Account{}, fmt.Errorf("get account %s: %w", code, err)
	}
	return acct, nil
}

// ListEntries returns the journal lines of a transaction in posting order.
func (s *Service) ListEntries(ctx context.Context, txID uuid.UUID) ([]ledgerdb.Entry, error) {
	return s.repo.Queries().ListEntriesByTransaction(ctx, txID)
}

// ExportTransactions maps every transaction initiated in [from, to) to a
// canonical.Record (FR-L5). source/destination/amount are derived from the
// journal entries: the amount is the transaction's total debits, the
// destination is the debited account, and the source is the credited account.
func (s *Service) ExportTransactions(ctx context.Context, from, to time.Time) ([]canonical.Record, error) {
	q := s.repo.Queries()
	txns, err := q.ListTransactionsByWindow(ctx, ledgerdb.ListTransactionsByWindowParams{
		InitiatedAt:   from,
		InitiatedAt_2: to,
	})
	if err != nil {
		return nil, fmt.Errorf("list transactions: %w", err)
	}

	codes := make(map[uuid.UUID]string)
	resolveCode := func(id uuid.UUID) (string, error) {
		if c, ok := codes[id]; ok {
			return c, nil
		}
		acct, err := q.GetAccountByID(ctx, id)
		if err != nil {
			return "", fmt.Errorf("resolve account %s: %w", id, err)
		}
		codes[id] = acct.Code
		return acct.Code, nil
	}

	records := make([]canonical.Record, 0, len(txns))
	for _, tx := range txns {
		entries, err := q.ListEntriesByTransaction(ctx, tx.ID)
		if err != nil {
			return nil, fmt.Errorf("list entries for %s: %w", tx.ID, err)
		}

		var amount int64
		var source, destination string
		for _, e := range entries {
			code, err := resolveCode(e.AccountID)
			if err != nil {
				return nil, err
			}
			if Direction(e.Direction) == Debit {
				amount += e.AmountMinor
				if destination == "" {
					destination = code
				}
			} else if source == "" {
				source = code
			}
		}

		meta, err := unmarshalMetadata(tx.Metadata)
		if err != nil {
			return nil, err
		}

		var settledAt *time.Time
		if tx.SettledAt != nil {
			u := tx.SettledAt.UTC()
			settledAt = &u
		}

		records = append(records, canonical.Record{
			TransactionID: tx.ID.String(),
			Reference:     tx.Reference,
			Source:        source,
			Destination:   destination,
			AmountMinor:   money.FromMinor(amount),
			Currency:      tx.Currency,
			Type:          canonical.Type(tx.Type),
			Status:        canonical.Status(tx.Status),
			InitiatedAt:   tx.InitiatedAt.UTC(),
			SettledAt:     settledAt,
			Metadata:      meta,
		})
	}
	return records, nil
}

func unmarshalMetadata(b []byte) (map[string]string, error) {
	if len(b) == 0 {
		return nil, nil
	}
	m := make(map[string]string)
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("unmarshal metadata: %w", err)
	}
	if len(m) == 0 {
		return nil, nil
	}
	return m, nil
}
