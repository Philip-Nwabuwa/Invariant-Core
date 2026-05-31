package ledger

import (
	"context"
	"testing"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
)

// TestGetBalance_DerivedEqualsCached posts a series of transfers and asserts the
// journal-derived balance equals the cached account_balances figure for every
// touched account (NS-103 / FR-L4).
func TestGetBalance_DerivedEqualsCached(t *testing.T) {
	svc, repo := newService(t)
	ctx := context.Background()

	// CUST-001 (asset) is seeded by newService; SETTLEMENT (liability) by the migration.
	amounts := []int64{5000, 1200, 9999, 3, 250000}
	for i, a := range amounts {
		ref := "REF-" + string(rune('A'+i))
		if _, err := svc.PostTransaction(ctx, PostRequest{
			Reference: ref,
			Type:      canonical.TypeTransfer,
			Entries: []EntryInput{
				entry("CUST-001", Debit, a),
				entry("SETTLEMENT", Credit, a),
			},
		}); err != nil {
			t.Fatalf("post %s: %v", ref, err)
		}
	}

	for _, code := range []string{"CUST-001", "SETTLEMENT"} {
		derived, err := svc.GetBalance(ctx, code)
		if err != nil {
			t.Fatalf("GetBalance(%s): %v", code, err)
		}
		acct, err := repo.Queries().GetAccountByCode(ctx, code)
		if err != nil {
			t.Fatalf("get account %s: %v", code, err)
		}
		cached, err := repo.Queries().GetCachedBalance(ctx, acct.ID)
		if err != nil {
			t.Fatalf("get cached balance %s: %v", code, err)
		}
		if derived.Minor() != cached.BalanceMinor {
			t.Fatalf("%s: derived=%d cached=%d (must match)", code, derived.Minor(), cached.BalanceMinor)
		}
	}
}
