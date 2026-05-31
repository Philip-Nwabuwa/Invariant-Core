package transfer_test

import (
	"context"
	"errors"
	"testing"

	ledgerv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/ledger/v1"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/testsupport"
	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// TestSettle_EndToEnd exercises the full async switch stack (IdempotentService ->
// Orchestrator -> Driver -> real ledger over bufconn + a success fake rail, all
// over one Postgres). POST debits and returns 202/DEBITED; the outbox poller
// settles; and the durable idempotency guarantees hold:
//
//   - the transfer settles and money moves through the ledger exactly once;
//   - replaying the same Idempotency-Key returns the same transfer's LIVE state
//     and creates no second transactions row;
//   - reusing the key with a different body is a conflict.
func TestSettle_EndToEnd(t *testing.T) {
	pool := testsupport.NewPool(t) // skips when Docker is unavailable
	ctx := context.Background()

	rawLedger := dialLedgerOn(t, pool)
	store := transfer.NewPostgresStore(pool)
	driver := transfer.NewDriver(store, transfer.NewLedgerClient(rawLedger), &fakeRail{verdict: transfer.VerdictSuccess})
	orchestrator := transfer.NewOrchestrator(store, driver)
	svc := transfer.NewIdempotentService(orchestrator, transfer.NewIdempotencyStore(pool))

	req := transfer.CreateRequest{
		Source:      "CUST-001",
		Destination: "CUST-002",
		Amount:      money.FromMinor(5000),
		Currency:    "NGN",
		Reference:   "REF-E2E",
	}
	const key = "key-e2e"

	// First call debits synchronously (202/DEBITED), then the poller settles.
	first, err := svc.Create(ctx, key, req)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if first.State != transfer.StateDebited {
		t.Fatalf("post-create state = %s, want DEBITED", first.State)
	}
	drainOutbox(t, store, driver)

	// Replay: same key + same body returns the same transfer's LIVE state (now
	// SETTLED, not the stored DEBITED response) and does no new work.
	replay, err := svc.Create(ctx, key, req)
	if err != nil {
		t.Fatalf("replay create: %v", err)
	}
	if replay.ID != first.ID {
		t.Errorf("replay id = %q, want %q (same transfer)", replay.ID, first.ID)
	}
	if replay.State != transfer.StateSettled {
		t.Errorf("replay state = %s, want live SETTLED", replay.State)
	}

	// Exactly one transactions row carries this customer key (DoD: no second
	// transaction). The ledger legs use distinct <id>:debit/<id>:settle keys.
	var count int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM transactions WHERE idempotency_key = $1", key,
	).Scan(&count); err != nil {
		t.Fatalf("count transactions: %v", err)
	}
	if count != 1 {
		t.Errorf("transactions for key = %d, want 1", count)
	}

	// Money moved exactly once: SETTLEMENT nets to zero, source debited once,
	// destination credited once.
	for code, want := range map[string]int64{"CUST-001": 5000, "CUST-002": -5000, "SETTLEMENT": 0} {
		bal, err := rawLedger.GetBalance(ctx, &ledgerv1.GetBalanceRequest{AccountCode: code})
		if err != nil {
			t.Fatalf("balance %s: %v", code, err)
		}
		if bal.GetBalanceMinor() != want {
			t.Errorf("%s balance = %d, want %d", code, bal.GetBalanceMinor(), want)
		}
	}

	// Same key, different body → conflict.
	altered := req
	altered.Amount = money.FromMinor(9999)
	if _, err := svc.Create(ctx, key, altered); !errors.Is(err, transfer.ErrIdempotencyConflict) {
		t.Fatalf("altered body reused key: err = %v, want ErrIdempotencyConflict", err)
	}
}
