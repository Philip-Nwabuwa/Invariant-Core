package transfer_test

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	switchv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/switch/v1"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/testsupport"
	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// TestCorrectiveReversal_RedrivesStrandedReversal proves the reconcile feedback
// loop (NS-501/502, AC-5): a transfer stranded in reversal_pending — its outbox
// event lost — is re-driven by the corrective endpoint, restoring the source
// exactly once. A second corrective call is a no-op, and an unknown reference is
// NotFound.
func TestCorrectiveReversal_RedrivesStrandedReversal(t *testing.T) {
	pool := testsupport.NewPool(t) // skips when Docker is unavailable
	ctx := context.Background()

	rawLedger := dialLedgerOn(t, pool)
	store := transfer.NewPostgresStore(pool)
	ledClient := transfer.NewLedgerClient(rawLedger)
	driver := transfer.NewDriver(store, ledClient, &fakeRail{verdict: transfer.VerdictDeclined})
	o := transfer.NewOrchestrator(store, driver)
	client := dialSwitch(t, driver)

	const ref = "REF-CORRECTIVE"
	req := transfer.CreateRequest{
		Source: "CUST-001", Destination: "CUST-002", Amount: money.FromMinor(5000), Currency: "NGN", Reference: ref,
	}

	// Create posts the debit leg synchronously (state DEBITED) and enqueues the
	// async settlement event. Drop that event, then declare the rail declined via
	// callback so the transfer parks in reversal_pending with the reversal enqueued.
	view, err := o.Create(ctx, "key-corrective", req)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM outbox WHERE published_at IS NULL`); err != nil {
		t.Fatalf("drop debited event: %v", err)
	}
	if _, err := client.RailCallback(ctx, &switchv1.RailCallbackRequest{
		Reference: ref, Status: switchv1.CallbackStatus_CALLBACK_STATUS_DECLINED,
	}); err != nil {
		t.Fatalf("declined callback: %v", err)
	}

	// Strand the reversal: drop its unpublished outbox row so the poller can never
	// run it (a lost event / crash window). Draining now does nothing.
	if _, err := pool.Exec(ctx, `DELETE FROM outbox WHERE published_at IS NULL`); err != nil {
		t.Fatalf("strand reversal event: %v", err)
	}
	drainOutbox(t, store, driver)
	if got, err := o.Get(ctx, view.ID); err != nil {
		t.Fatalf("get (stranded): %v", err)
	} else if got.State != transfer.StateReversalPending {
		t.Fatalf("stranded state = %s, want REVERSAL_PENDING", got.State)
	}

	// Corrective feedback re-enqueues the reversal; draining then posts it.
	resp, err := client.CorrectiveReversal(ctx, &switchv1.CorrectiveReversalRequest{Reference: ref, Reason: "recon pending_reversal"})
	if err != nil {
		t.Fatalf("corrective reversal: %v", err)
	}
	if !resp.GetRequeued() {
		t.Fatalf("requeued = false, want true for a stranded reversal_pending")
	}
	drainOutbox(t, store, driver)

	got, err := o.Get(ctx, view.ID)
	if err != nil {
		t.Fatalf("get (corrected): %v", err)
	}
	if got.State != transfer.StateReversed {
		t.Fatalf("corrected state = %s, want REVERSED", got.State)
	}

	// Source restored exactly once, destination untouched, settlement nets to zero.
	assertBalances(t, rawLedger, map[string]int64{"CUST-001": 0, "CUST-002": 0, "SETTLEMENT": 0})

	var reversalCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM transactions WHERE type='reversal' AND reference=$1`, ref).
		Scan(&reversalCount); err != nil {
		t.Fatalf("count reversal: %v", err)
	}
	if reversalCount != 1 {
		t.Fatalf("reversal rows = %d, want 1", reversalCount)
	}

	// A second corrective call is an idempotent no-op (already reversed).
	resp2, err := client.CorrectiveReversal(ctx, &switchv1.CorrectiveReversalRequest{Reference: ref})
	if err != nil {
		t.Fatalf("second corrective: %v", err)
	}
	if resp2.GetRequeued() {
		t.Errorf("requeued = true on a reversed transfer, want false (no-op)")
	}
	if resp2.GetState() != string(transfer.StateReversed) {
		t.Errorf("state = %s, want REVERSED", resp2.GetState())
	}

	// Unknown reference is NotFound.
	if _, err := client.CorrectiveReversal(ctx, &switchv1.CorrectiveReversalRequest{Reference: "does-not-exist"}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown reference err = %v, want NotFound", err)
	}
}
