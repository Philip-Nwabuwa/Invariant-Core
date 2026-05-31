package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/ledgerdb"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/testsupport"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

func entry(code string, dir Direction, minor int64) EntryInput {
	return EntryInput{AccountCode: code, Direction: dir, Amount: money.FromMinor(minor), Currency: "NGN"}
}

// TestValidate covers the pure double-entry invariants (no database needed).
func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		req     PostRequest
		wantErr error
	}{
		{
			name:    "balanced two-entry transfer",
			req:     PostRequest{Entries: []EntryInput{entry("A", Debit, 5000), entry("B", Credit, 5000)}},
			wantErr: nil,
		},
		{
			name:    "single entry rejected",
			req:     PostRequest{Entries: []EntryInput{entry("A", Debit, 5000)}},
			wantErr: ErrTooFewEntries,
		},
		{
			name:    "unbalanced rejected",
			req:     PostRequest{Entries: []EntryInput{entry("A", Debit, 5000), entry("B", Credit, 4000)}},
			wantErr: ErrUnbalanced,
		},
		{
			name: "mixed currency rejected",
			req: PostRequest{Entries: []EntryInput{
				entry("A", Debit, 5000),
				{AccountCode: "B", Direction: Credit, Amount: money.FromMinor(5000), Currency: "USD"},
			}},
			wantErr: ErrMixedCurrency,
		},
		{
			name:    "non-positive amount rejected",
			req:     PostRequest{Entries: []EntryInput{entry("A", Debit, 0), entry("B", Credit, 0)}},
			wantErr: ErrNonPositiveAmount,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validate(tt.req); !errors.Is(err, tt.wantErr) {
				t.Fatalf("validate() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// newService spins up a Postgres container, seeds a customer account, and
// returns a wired Service plus its repository.
func newService(t *testing.T) (*Service, *postgres.Repository) {
	t.Helper()
	pool := testsupport.NewPool(t)
	repo := postgres.NewRepository(pool)
	if _, err := repo.Queries().CreateAccount(context.Background(), ledgerdb.CreateAccountParams{
		Code: "CUST-001", Name: "Customer 001", Type: "asset", Currency: "NGN",
	}); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	return NewService(repo), repo
}

func TestPostTransaction_BalancedSucceeds(t *testing.T) {
	svc, repo := newService(t)
	ctx := context.Background()

	txID, err := svc.PostTransaction(ctx, PostRequest{
		Reference: "REF-1",
		Type:      canonical.TypeTransfer,
		Entries: []EntryInput{
			entry("CUST-001", Debit, 5000),
			entry("SETTLEMENT", Credit, 5000),
		},
	})
	if err != nil {
		t.Fatalf("PostTransaction: %v", err)
	}

	entries, err := repo.Queries().ListEntriesByTransaction(ctx, txID)
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}

	tx, err := repo.Queries().GetTransaction(ctx, txID)
	if err != nil {
		t.Fatalf("get transaction: %v", err)
	}
	if tx.Status != string(canonical.StatusPending) {
		t.Fatalf("status = %q, want pending", tx.Status)
	}
}

func TestPostTransaction_UnknownAccount(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.PostTransaction(context.Background(), PostRequest{
		Reference: "REF-2",
		Type:      canonical.TypeTransfer,
		Entries: []EntryInput{
			entry("CUST-001", Debit, 5000),
			entry("DOES-NOT-EXIST", Credit, 5000),
		},
	})
	if !errors.Is(err, ErrUnknownAccount) {
		t.Fatalf("err = %v, want ErrUnknownAccount", err)
	}
}
