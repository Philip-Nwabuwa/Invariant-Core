package transfer_test

import (
	"context"
	"testing"

	ledgerv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/ledger/v1"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/testsupport"
	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// TestRecovery_ResumesStrandedDebit simulates a crash that lost a transfer's
// outbox event: the transfer is debited but has no event to drive it forward.
// The recovery sweep re-enqueues it and the poller settles it — the debit is
// never stranded, and exactly one debit was posted.
func TestRecovery_ResumesStrandedDebit(t *testing.T) {
	pool := testsupport.NewPool(t) // skips when Docker is unavailable
	ctx := context.Background()
	rawLedger := dialLedgerOn(t, pool)
	store := transfer.NewPostgresStore(pool)
	driver := transfer.NewDriver(store, transfer.NewLedgerClient(rawLedger), &fakeRail{verdict: transfer.VerdictSuccess})
	o := transfer.NewOrchestrator(store, driver)

	const ref = "REF-RECOVERY"
	view, err := o.Create(ctx, "key-recovery", transfer.CreateRequest{
		Source: "CUST-001", Destination: "CUST-002", Amount: money.FromMinor(5000), Currency: "NGN", Reference: ref,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// view is DEBITED; the debit posted and a transfer.debited event is queued.

	// Simulate the crash losing every outbox event for this transfer.
	if _, err := pool.Exec(ctx, `DELETE FROM outbox WHERE aggregate_id = $1`, view.ID); err != nil {
		t.Fatalf("delete outbox: %v", err)
	}
	// Draining now does nothing — the transfer is stranded at DEBITED.
	drainOutbox(t, store, driver)
	if got, _ := o.Get(ctx, view.ID); got.State != transfer.StateDebited {
		t.Fatalf("pre-recovery state = %s, want DEBITED (stranded)", got.State)
	}

	// Recovery re-enqueues the driving event; the poller then settles it.
	n, err := transfer.NewRecoverer(store).Recover(ctx)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if n != 1 {
		t.Fatalf("recovered = %d, want 1", n)
	}
	drainOutbox(t, store, driver)

	if got, _ := o.Get(ctx, view.ID); got.State != transfer.StateSettled {
		t.Fatalf("post-recovery state = %s, want SETTLED", got.State)
	}
	// Exactly one debit leg, and SETTLEMENT nets to zero.
	var debitLegs int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM transactions WHERE reference=$1 AND idempotency_key LIKE '%:debit'`, ref).
		Scan(&debitLegs); err != nil {
		t.Fatalf("count debit legs: %v", err)
	}
	if debitLegs != 1 {
		t.Errorf("debit legs = %d, want 1 (no double debit)", debitLegs)
	}
	assertBalances(t, rawLedger, map[string]int64{"CUST-001": 5000, "CUST-002": -5000, "SETTLEMENT": 0})
}

// TestIdempotency_LeaseTakeover: a replay of an in-progress key whose lease has
// expired (the original holder crashed) re-attaches to the transfer that key
// created and returns its live state — it never starts a second transfer.
func TestIdempotency_LeaseTakeover(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()
	rawLedger := dialLedgerOn(t, pool)
	store := transfer.NewPostgresStore(pool)
	driver := transfer.NewDriver(store, transfer.NewLedgerClient(rawLedger), &fakeRail{verdict: transfer.VerdictSuccess})
	o := transfer.NewOrchestrator(store, driver)
	idem := transfer.NewIdempotencyStore(pool)
	svc := transfer.NewIdempotentService(o, idem)

	req := transfer.CreateRequest{
		Source: "CUST-001", Destination: "CUST-002", Amount: money.FromMinor(5000), Currency: "NGN", Reference: "REF-LEASE",
	}
	const key = "key-lease"

	// Simulate a holder that reserved the key and created the transfer, then
	// crashed before completing — an in-progress record whose lease has expired,
	// with no transaction_id linked (the transfer row carries the key instead).
	transferID, err := store.CreatePending(ctx, key, req)
	if err != nil {
		t.Fatalf("create pending: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO idempotency_keys (key, request_fingerprint, status, expires_at)
		 VALUES ($1, $2, 'in_progress', now() - interval '1 minute')`,
		key, transfer.Fingerprint(req)); err != nil {
		t.Fatalf("seed expired in-progress key: %v", err)
	}

	// A replay past the lease takes over: it returns the existing transfer's live
	// view (not a conflict/in-progress error) and starts no second transfer.
	view, err := svc.Create(ctx, key, req)
	if err != nil {
		t.Fatalf("takeover create: %v", err)
	}
	if view.ID != transferID.String() {
		t.Errorf("takeover id = %s, want existing transfer %s", view.ID, transferID)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM transactions WHERE idempotency_key = $1`, key).Scan(&count); err != nil {
		t.Fatalf("count transfers: %v", err)
	}
	if count != 1 {
		t.Errorf("transfers for key = %d, want 1 (no second transfer)", count)
	}
	_ = ledgerv1.GetBalanceRequest{}
}
