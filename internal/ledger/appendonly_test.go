package ledger

import (
	"context"
	"testing"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
)

// TestEntries_AppendOnly asserts the DB trigger trg_entries_no_update rejects
// any raw UPDATE or DELETE on entries (FR-L3). The domain layer only ever
// inserts entries; corrections are compensating transactions, never mutations.
func TestEntries_AppendOnly(t *testing.T) {
	svc, repo := newService(t)
	ctx := context.Background()

	txID, err := svc.PostTransaction(ctx, PostRequest{
		Reference: "REF-AO",
		Type:      canonical.TypeTransfer,
		Entries: []EntryInput{
			entry("CUST-001", Debit, 5000),
			entry("SETTLEMENT", Credit, 5000),
		},
	})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	entries, err := repo.Queries().ListEntriesByTransaction(ctx, txID)
	if err != nil || len(entries) == 0 {
		t.Fatalf("list entries: %v (n=%d)", err, len(entries))
	}
	id := entries[0].ID

	if _, err := repo.Pool().Exec(ctx,
		`UPDATE entries SET amount_minor = amount_minor + 1 WHERE id = $1`, id); err == nil {
		t.Fatal("UPDATE on entries succeeded; expected trigger rejection")
	}

	if _, err := repo.Pool().Exec(ctx, `DELETE FROM entries WHERE id = $1`, id); err == nil {
		t.Fatal("DELETE on entries succeeded; expected trigger rejection")
	}

	// The row is untouched.
	after, err := repo.Queries().ListEntriesByTransaction(ctx, txID)
	if err != nil {
		t.Fatalf("list entries after: %v", err)
	}
	if len(after) != len(entries) {
		t.Fatalf("entry count changed: before=%d after=%d", len(entries), len(after))
	}
}
