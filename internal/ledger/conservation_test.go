package ledger

import (
	"context"
	"fmt"
	"testing"

	"pgregory.net/rapid"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/ledgerdb"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/testsupport"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
)

// TestProperty_ValueConserved is the AC-2 conservation property: fund N customer
// accounts with random opening balances, then apply random *balanced* transfers
// among them. Because every posting is balanced, the total money held across the
// customer accounts must always equal the starting total — value is neither
// created nor destroyed.
func TestProperty_ValueConserved(t *testing.T) {
	pool := testsupport.NewPool(t)
	svc := NewService(postgres.NewRepository(pool))
	ctx := context.Background()
	iter := 0

	rapid.Check(t, func(rt *rapid.T) {
		iter++
		prefix := fmt.Sprintf("P%d-", iter)

		n := rapid.IntRange(2, 5).Draw(rt, "accounts")
		custCodes := make([]string, n)
		for i := range custCodes {
			custCodes[i] = fmt.Sprintf("%sCUST-%d", prefix, i)
			mustCreate(t, svc, custCodes[i], "asset")
		}
		opening := prefix + "OPENING"
		mustCreate(t, svc, opening, "equity")

		// Fund each customer with a random opening balance (debit asset, credit equity).
		var startingTotal int64
		for i, code := range custCodes {
			amt := rapid.Int64Range(1, 1_000_000).Draw(rt, fmt.Sprintf("open-%d", i))
			startingTotal += amt
			mustPost(t, svc, prefix+fmt.Sprintf("open-%d", i), []EntryInput{
				entry(code, Debit, amt),
				entry(opening, Credit, amt),
			})
		}

		// Apply random balanced transfers strictly among the customer accounts.
		m := rapid.IntRange(0, 20).Draw(rt, "transfers")
		for j := 0; j < m; j++ {
			src := rapid.IntRange(0, n-1).Draw(rt, fmt.Sprintf("src-%d", j))
			dst := rapid.IntRange(0, n-1).Draw(rt, fmt.Sprintf("dst-%d", j))
			if src == dst {
				continue
			}
			amt := rapid.Int64Range(1, 1_000_000).Draw(rt, fmt.Sprintf("amt-%d", j))
			// Money leaves src (credit, asset decreases) and lands in dst (debit).
			mustPost(t, svc, prefix+fmt.Sprintf("xfer-%d", j), []EntryInput{
				entry(custCodes[dst], Debit, amt),
				entry(custCodes[src], Credit, amt),
			})
		}

		var total int64
		for _, code := range custCodes {
			bal, err := svc.GetBalance(ctx, code)
			if err != nil {
				t.Fatalf("GetBalance(%s): %v", code, err)
			}
			total += bal.Minor()
		}
		if total != startingTotal {
			t.Fatalf("conservation violated: total=%d startingTotal=%d", total, startingTotal)
		}
	})
}

// TestProperty_UnbalancedRejected is the AC-2 safety property: any unbalanced
// posting is rejected and never commits a transaction.
func TestProperty_UnbalancedRejected(t *testing.T) {
	pool := testsupport.NewPool(t)
	repo := postgres.NewRepository(pool)
	svc := NewService(repo)
	ctx := context.Background()
	mustCreate(t, svc, "CUST-001", "asset")

	rapid.Check(t, func(rt *rapid.T) {
		debit := rapid.Int64Range(1, 1_000_000).Draw(rt, "debit")
		delta := rapid.Int64Range(1, 1_000_000).Draw(rt, "delta")
		credit := debit + delta // guaranteed != debit

		_, err := svc.PostTransaction(ctx, PostRequest{
			Reference: "UNBAL",
			Type:      canonical.TypeTransfer,
			Entries: []EntryInput{
				entry("CUST-001", Debit, debit),
				entry("SETTLEMENT", Credit, credit),
			},
		})
		if err == nil {
			t.Fatal("unbalanced post succeeded; must be rejected")
		}

		// Nothing was committed under this reference.
		var count int64
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM transactions WHERE reference = 'UNBAL'`).Scan(&count); err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 0 {
			t.Fatalf("unbalanced transaction was committed: count=%d", count)
		}
	})
}

func mustCreate(t *testing.T, svc *Service, code, accType string) {
	t.Helper()
	if _, err := svc.repo.Queries().CreateAccount(context.Background(), ledgerdb.CreateAccountParams{
		Code: code, Name: code, Type: accType, Currency: "NGN",
	}); err != nil {
		t.Fatalf("create account %s: %v", code, err)
	}
}

func mustPost(t *testing.T, svc *Service, ref string, entries []EntryInput) {
	t.Helper()
	if _, err := svc.PostTransaction(context.Background(), PostRequest{
		Reference: ref, Type: canonical.TypeTransfer, Entries: entries,
	}); err != nil {
		t.Fatalf("post %s: %v", ref, err)
	}
}
