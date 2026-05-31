package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/ledgerdb"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/testsupport"
)

// TestRepository_PostAndReadBack exercises NS-101: create an account, post a
// balanced transaction with two entries inside a SERIALIZABLE tx, then read the
// rows back through the pool.
func TestRepository_PostAndReadBack(t *testing.T) {
	pool := testsupport.NewPool(t)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	q := repo.Queries()

	// SETTLEMENT is seeded by the migration; create a customer account.
	cust, err := q.CreateAccount(ctx, ledgerdb.CreateAccountParams{
		Code: "CUST-001", Name: "Customer 001", Type: "asset", Currency: "NGN",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	settlement, err := q.GetAccountByCode(ctx, "SETTLEMENT")
	if err != nil {
		t.Fatalf("get SETTLEMENT: %v", err)
	}

	var txID uuid.UUID
	err = repo.WithSerializableTx(ctx, func(q *ledgerdb.Queries) error {
		tx, err := q.InsertTransaction(ctx, ledgerdb.InsertTransactionParams{
			Reference: "REF-1", Type: "transfer", Status: "pending",
			Currency: "NGN", Metadata: []byte("{}"),
		})
		if err != nil {
			return err
		}
		txID = tx.ID
		if _, err := q.InsertEntry(ctx, ledgerdb.InsertEntryParams{
			TransactionID: tx.ID, AccountID: cust.ID,
			Direction: "debit", AmountMinor: 5000, Currency: "NGN",
		}); err != nil {
			return err
		}
		_, err = q.InsertEntry(ctx, ledgerdb.InsertEntryParams{
			TransactionID: tx.ID, AccountID: settlement.ID,
			Direction: "credit", AmountMinor: 5000, Currency: "NGN",
		})
		return err
	})
	if err != nil {
		t.Fatalf("post transaction: %v", err)
	}

	entries, err := q.ListEntriesByTransaction(ctx, txID)
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}

	sum, err := q.SumEntriesByAccount(ctx, cust.ID)
	if err != nil {
		t.Fatalf("sum entries: %v", err)
	}
	if sum.DebitMinor != 5000 || sum.CreditMinor != 0 {
		t.Fatalf("customer sums: got debit=%d credit=%d, want 5000/0", sum.DebitMinor, sum.CreditMinor)
	}
}
