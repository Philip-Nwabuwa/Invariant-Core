// Package ledger is the double-entry ledger domain: it validates and posts
// balanced journal entries at SERIALIZABLE isolation (ADR-0002) and derives
// account balances from the journal, which is the single source of truth.
package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/ledgerdb"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
)

// Posting validation errors.
var (
	// ErrTooFewEntries means a request had fewer than two entries.
	ErrTooFewEntries = errors.New("ledger: transaction needs at least two entries")
	// ErrUnbalanced means sum(debits) != sum(credits).
	ErrUnbalanced = errors.New("ledger: transaction is unbalanced")
	// ErrMixedCurrency means the entries do not all share one currency.
	ErrMixedCurrency = errors.New("ledger: transaction mixes currencies")
	// ErrNonPositiveAmount means an entry's amount was not strictly positive.
	ErrNonPositiveAmount = errors.New("ledger: entry amount must be positive")
	// ErrUnknownAccount means an entry referenced an account code that does not exist.
	ErrUnknownAccount = errors.New("ledger: unknown account")
)

// maxSerializationRetries bounds the retry loop on SQLSTATE 40001 (ADR-0002).
const maxSerializationRetries = 5

// Service is the ledger's domain API.
type Service struct {
	repo *postgres.Repository
}

// NewService builds a Service over the given repository.
func NewService(repo *postgres.Repository) *Service {
	return &Service{repo: repo}
}

// PostTransaction validates the request and records it as a balanced set of
// journal entries inside a single SERIALIZABLE transaction, updating the cached
// balance of every touched account in that same transaction. Serialization
// failures are retried with bounded backoff. It returns the new transaction id.
func (s *Service) PostTransaction(ctx context.Context, req PostRequest) (uuid.UUID, error) {
	if err := validate(req); err != nil {
		return uuid.Nil, err
	}

	metadata, err := marshalMetadata(req.Metadata)
	if err != nil {
		return uuid.Nil, err
	}

	currency := req.Entries[0].Currency
	status := string(req.Status)
	if status == "" {
		status = string(canonical.StatusPending)
	}

	var txID uuid.UUID
	err = retryOnSerialization(ctx, maxSerializationRetries, func() error {
		return s.repo.WithSerializableTx(ctx, func(q *ledgerdb.Queries) error {
			accounts, err := resolveAccounts(ctx, q, req.Entries)
			if err != nil {
				return err
			}

			tx, err := q.InsertTransaction(ctx, ledgerdb.InsertTransactionParams{
				Reference: req.Reference,
				Type:      string(req.Type),
				Status:    status,
				Currency:  currency,
				Metadata:  metadata,
			})
			if err != nil {
				return fmt.Errorf("insert transaction: %w", err)
			}

			for _, e := range req.Entries {
				if _, err := q.InsertEntry(ctx, ledgerdb.InsertEntryParams{
					TransactionID: tx.ID,
					AccountID:     accounts[e.AccountCode].ID,
					Direction:     string(e.Direction),
					AmountMinor:   e.Amount.Minor(),
					Currency:      e.Currency,
				}); err != nil {
					return fmt.Errorf("insert entry: %w", err)
				}
			}

			if err := updateCachedBalances(ctx, q, accounts); err != nil {
				return err
			}

			txID = tx.ID
			return nil
		})
	})
	if err != nil {
		return uuid.Nil, err
	}
	return txID, nil
}

// validate enforces the double-entry invariants that don't require the database:
// at least two entries, a single currency, positive amounts, and balance.
func validate(req PostRequest) error {
	if len(req.Entries) < 2 {
		return ErrTooFewEntries
	}
	currency := req.Entries[0].Currency
	for _, e := range req.Entries {
		if e.Currency != currency {
			return ErrMixedCurrency
		}
		if e.Amount.Minor() <= 0 {
			return ErrNonPositiveAmount
		}
	}
	debits, credits := req.totals()
	if debits != credits {
		return ErrUnbalanced
	}
	return nil
}

// resolveAccounts maps each referenced code to its account row, failing with
// ErrUnknownAccount on the first code that does not exist.
func resolveAccounts(ctx context.Context, q *ledgerdb.Queries, entries []EntryInput) (map[string]ledgerdb.Account, error) {
	accounts := make(map[string]ledgerdb.Account)
	for _, e := range entries {
		if _, ok := accounts[e.AccountCode]; ok {
			continue
		}
		acct, err := q.GetAccountByCode(ctx, e.AccountCode)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("%w: %s", ErrUnknownAccount, e.AccountCode)
			}
			return nil, fmt.Errorf("resolve account %s: %w", e.AccountCode, err)
		}
		accounts[e.AccountCode] = acct
	}
	return accounts, nil
}

// updateCachedBalances re-derives and upserts the cached balance of every
// touched account from the journal, inside the caller's transaction.
func updateCachedBalances(ctx context.Context, q *ledgerdb.Queries, accounts map[string]ledgerdb.Account) error {
	for _, acct := range accounts {
		sum, err := q.SumEntriesByAccount(ctx, acct.ID)
		if err != nil {
			return fmt.Errorf("sum entries for %s: %w", acct.Code, err)
		}
		balance := DeriveBalance(AccountType(acct.Type), sum.DebitMinor, sum.CreditMinor)
		if err := q.UpsertCachedBalance(ctx, ledgerdb.UpsertCachedBalanceParams{
			AccountID:    acct.ID,
			BalanceMinor: balance.Minor(),
			Currency:     acct.Currency,
		}); err != nil {
			return fmt.Errorf("upsert balance for %s: %w", acct.Code, err)
		}
	}
	return nil
}

func marshalMetadata(m map[string]string) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal metadata: %w", err)
	}
	return b, nil
}
