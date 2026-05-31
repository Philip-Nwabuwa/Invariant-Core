package transfer_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	ledgerv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/ledger/v1"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/testsupport"
	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// TestReversal_RestoresSourceExactlyOnce drives a transfer the rail declines:
// the switch posts a compensating reversal that restores the source, links it to
// the debit leg, leaves the destination untouched, and never double-reverses
// even when the reversal is re-driven.
func TestReversal_RestoresSourceExactlyOnce(t *testing.T) {
	pool := testsupport.NewPool(t) // skips when Docker is unavailable
	ctx := context.Background()

	rawLedger := dialLedgerOn(t, pool)
	store := transfer.NewPostgresStore(pool)
	ledClient := transfer.NewLedgerClient(rawLedger)
	driver := transfer.NewDriver(store, ledClient, &fakeRail{verdict: transfer.VerdictDeclined})
	o := transfer.NewOrchestrator(store, driver)

	req := transfer.CreateRequest{
		Source:      "CUST-001",
		Destination: "CUST-002",
		Amount:      money.FromMinor(5000),
		Currency:    "NGN",
		Reference:   "REF-REVERSAL",
	}

	view, err := o.Create(ctx, "key-rev", req)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	drainOutbox(t, store, driver)

	got, err := o.Get(ctx, view.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != transfer.StateReversed {
		t.Fatalf("state = %s, want REVERSED", got.State)
	}

	// Source restored exactly, destination untouched, settlement nets to zero.
	for code, want := range map[string]int64{"CUST-001": 0, "CUST-002": 0, "SETTLEMENT": 0} {
		bal, err := rawLedger.GetBalance(ctx, &ledgerv1.GetBalanceRequest{AccountCode: code})
		if err != nil {
			t.Fatalf("balance %s: %v", code, err)
		}
		if bal.GetBalanceMinor() != want {
			t.Errorf("%s balance = %d, want %d", code, bal.GetBalanceMinor(), want)
		}
	}

	// Exactly one reversal exists, parent-linked to the debit leg.
	transferID := uuid.MustParse(view.ID)
	var reversalCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM transactions WHERE type='reversal' AND reference=$1`, req.Reference).
		Scan(&reversalCount); err != nil {
		t.Fatalf("count reversal: %v", err)
	}
	if reversalCount != 1 {
		t.Fatalf("reversal rows = %d, want 1", reversalCount)
	}
	var parentID, debitLegID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT parent_transaction_id FROM transactions WHERE type='reversal' AND reference=$1`, req.Reference).
		Scan(&parentID); err != nil {
		t.Fatalf("query reversal parent: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT (metadata->>'debit_leg_tx_id')::uuid FROM transactions WHERE id=$1`, transferID).
		Scan(&debitLegID); err != nil {
		t.Fatalf("read debit leg id: %v", err)
	}
	if parentID != debitLegID {
		t.Errorf("reversal parent = %s, want debit leg %s", parentID, debitLegID)
	}

	// Idempotent at the ledger: re-posting the same reversal leg is a no-op, so
	// the compensating transaction is never duplicated (uq_reversal_per_parent /
	// the per-leg idempotency key). Re-driving the handler is likewise a no-op.
	tr := transfer.Transfer{ID: transferID, Reference: req.Reference, Source: "CUST-001", Destination: "CUST-002", Amount: money.FromMinor(5000), Currency: "NGN"}
	if err := ledClient.PostReversal(ctx, tr, debitLegID); err != nil {
		t.Fatalf("re-post reversal (want no-op): %v", err)
	}
	var after int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM transactions WHERE type='reversal' AND reference=$1`, req.Reference).
		Scan(&after); err != nil {
		t.Fatalf("recount reversal: %v", err)
	}
	if after != 1 {
		t.Errorf("reversal rows after re-post = %d, want 1 (idempotent)", after)
	}
}
