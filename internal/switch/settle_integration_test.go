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

// TestSettle_EndToEnd is the NS-205 acceptance test: the full switch stack
// (IdempotentService -> Orchestrator -> real ledger over bufconn + a fake rail,
// all over one Postgres) settles a transfer, and the durable idempotency
// guarantees from the DoD hold:
//
//   - a happy-path transfer settles and money moves through the ledger exactly once;
//   - replaying the same Idempotency-Key returns the same transfer and creates no
//     second transactions row;
//   - reusing the key with a different body is a conflict (HTTP 409).
func TestSettle_EndToEnd(t *testing.T) {
	pool := testsupport.NewPool(t) // skips when Docker is unavailable
	ctx := context.Background()

	rawLedger := dialLedgerOn(t, pool)
	orchestrator := transfer.NewOrchestrator(
		transfer.NewPostgresStore(pool),
		transfer.NewLedgerClient(rawLedger),
		&fakeRail{},
	)
	svc := transfer.NewIdempotentService(orchestrator, transfer.NewIdempotencyStore(pool))

	req := transfer.CreateRequest{
		Source:      "CUST-001",
		Destination: "CUST-002",
		Amount:      money.FromMinor(5000),
		Currency:    "NGN",
		Reference:   "REF-E2E",
	}
	const key = "key-e2e"

	// First call settles.
	first, err := svc.Create(ctx, key, req)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if first.State != transfer.StateSettled {
		t.Fatalf("state = %s, want SETTLED", first.State)
	}

	// Replay: same key + same body returns the same transfer, no new work.
	replay, err := svc.Create(ctx, key, req)
	if err != nil {
		t.Fatalf("replay create: %v", err)
	}
	if replay.ID != first.ID {
		t.Errorf("replay id = %q, want %q (same transfer)", replay.ID, first.ID)
	}

	// Exactly one transactions row exists for this key (DoD: no second transaction).
	var count int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM transactions WHERE idempotency_key = $1", key,
	).Scan(&count); err != nil {
		t.Fatalf("count transactions: %v", err)
	}
	if count != 1 {
		t.Errorf("transactions for key = %d, want 1", count)
	}

	// Money moved exactly once: SETTLEMENT nets to zero (credited then debited),
	// source debited once, destination credited once.
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
