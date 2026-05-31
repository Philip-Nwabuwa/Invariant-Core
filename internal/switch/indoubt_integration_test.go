package transfer_test

import (
	"context"
	"testing"

	ledgerv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/ledger/v1"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/testsupport"
	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// assertBalances checks each account's ledger-derived balance.
func assertBalances(t *testing.T, client ledgerv1.LedgerServiceClient, want map[string]int64) {
	t.Helper()
	ctx := context.Background()
	for code, w := range want {
		bal, err := client.GetBalance(ctx, &ledgerv1.GetBalanceRequest{AccountCode: code})
		if err != nil {
			t.Fatalf("balance %s: %v", code, err)
		}
		if bal.GetBalanceMinor() != w {
			t.Errorf("%s balance = %d, want %d", code, bal.GetBalanceMinor(), w)
		}
	}
}

// TestInDoubt_TSQConfirmsSettled is the case TSQ exists to catch: the rail timed
// out but had actually settled. The switch must complete settlement — destination
// credited, source NOT refunded — not reverse. Aggregate conservation alone would
// not distinguish this from a wrong reversal, so we assert the endpoints.
func TestInDoubt_TSQConfirmsSettled(t *testing.T) {
	pool := testsupport.NewPool(t) // skips when Docker is unavailable
	ctx := context.Background()
	rawLedger := dialLedgerOn(t, pool)
	store := transfer.NewPostgresStore(pool)
	rail := &fakeRail{verdict: transfer.VerdictUnknown, tsqVerdict: transfer.VerdictSuccess}
	driver := transfer.NewDriver(store, transfer.NewLedgerClient(rawLedger), rail, transfer.WithTSQPolicy(3, 0))
	o := transfer.NewOrchestrator(store, driver)

	view, err := o.Create(ctx, "key-id-settled", transfer.CreateRequest{
		Source: "CUST-001", Destination: "CUST-002", Amount: money.FromMinor(5000), Currency: "NGN", Reference: "REF-ID-SETTLED",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	drainOutbox(t, store, driver)

	got, err := o.Get(ctx, view.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != transfer.StateSettled {
		t.Fatalf("state = %s, want SETTLED (TSQ confirmed settlement)", got.State)
	}
	// Destination credited, source NOT refunded — the precise wrong-reversal bug.
	assertBalances(t, rawLedger, map[string]int64{"CUST-001": 5000, "CUST-002": -5000, "SETTLEMENT": 0})
}

// TestInDoubt_TSQConfirmsNoSettlement: the TSQ confirms the rail did not settle,
// so the switch reverses — source restored, destination untouched.
func TestInDoubt_TSQConfirmsNoSettlement(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()
	rawLedger := dialLedgerOn(t, pool)
	store := transfer.NewPostgresStore(pool)
	rail := &fakeRail{verdict: transfer.VerdictUnknown, tsqVerdict: transfer.VerdictDeclined}
	driver := transfer.NewDriver(store, transfer.NewLedgerClient(rawLedger), rail, transfer.WithTSQPolicy(3, 0))
	o := transfer.NewOrchestrator(store, driver)

	view, err := o.Create(ctx, "key-id-reversed", transfer.CreateRequest{
		Source: "CUST-001", Destination: "CUST-002", Amount: money.FromMinor(5000), Currency: "NGN", Reference: "REF-ID-REVERSED",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	drainOutbox(t, store, driver)

	got, err := o.Get(ctx, view.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != transfer.StateReversed {
		t.Fatalf("state = %s, want REVERSED (TSQ confirmed no settlement)", got.State)
	}
	assertBalances(t, rawLedger, map[string]int64{"CUST-001": 0, "CUST-002": 0, "SETTLEMENT": 0})
}

// TestInDoubt_TSQInconclusiveHolds: the TSQ never gives a definitive answer, so
// the transfer is held for MANUAL_REVIEW rather than guessed either way. The
// money stays in suspense (SETTLEMENT), neither settled nor reversed.
func TestInDoubt_TSQInconclusiveHolds(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()
	rawLedger := dialLedgerOn(t, pool)
	store := transfer.NewPostgresStore(pool)
	rail := &fakeRail{verdict: transfer.VerdictUnknown, tsqVerdict: transfer.VerdictUnknown}
	driver := transfer.NewDriver(store, transfer.NewLedgerClient(rawLedger), rail, transfer.WithTSQPolicy(2, 0))
	o := transfer.NewOrchestrator(store, driver)

	view, err := o.Create(ctx, "key-id-manual", transfer.CreateRequest{
		Source: "CUST-001", Destination: "CUST-002", Amount: money.FromMinor(5000), Currency: "NGN", Reference: "REF-ID-MANUAL",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	drainOutbox(t, store, driver)

	got, err := o.Get(ctx, view.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != transfer.StateManualReview {
		t.Fatalf("state = %s, want MANUAL_REVIEW", got.State)
	}
	if rail.tsqCalls != 2 {
		t.Errorf("TSQ attempts = %d, want 2", rail.tsqCalls)
	}
	// Money is held in suspense — debited but neither settled nor reversed.
	assertBalances(t, rawLedger, map[string]int64{"CUST-001": 5000, "CUST-002": 0, "SETTLEMENT": 5000})
}
