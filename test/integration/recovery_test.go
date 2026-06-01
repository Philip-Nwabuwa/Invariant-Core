//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/testsupport"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/mockrail"
	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/switch/outbox"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// TestReversalRecoveryAfterRestart simulates a switchd crash between debit and
// settlement: the transfer is debited, its in-flight outbox event is lost, and
// the rail declines. On "restart" the recovery sweep re-enqueues the transfer
// and the poller drives it to a completed reversal — the source is restored and
// no debit is left stranded.
func TestReversalRecoveryAfterRestart(t *testing.T) {
	pool := testsupport.NewPool(t) // skips when Docker is unavailable
	ctx := context.Background()

	ledgerClient := dialLedger(t, pool)
	// An always-decline rail so the recovered transfer routes to reversal.
	railClient := transfer.NewRailClient(dialRail(t, mockrail.NewServerWithConfig(mockrail.Config{Seed: 1, PDecline: 1.0})))
	svc, driver, store := newIdempotentSwitch(pool, transfer.NewLedgerClient(ledgerClient), railClient)

	const ref = "REF-INT-RECOVERY"
	view, err := svc.Create(ctx, "key-int-recovery", transfer.CreateRequest{
		Source: srcAccount, Destination: dstAccount, Amount: money.FromMinor(5000), Currency: "NGN", Reference: ref,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if view.State != transfer.StateDebited {
		t.Fatalf("post-create state = %s, want DEBITED", view.State)
	}

	// CRASH: drop the in-flight outbox event so nothing drives the transfer.
	if _, err := pool.Exec(ctx, `DELETE FROM outbox WHERE published_at IS NULL`); err != nil {
		t.Fatalf("simulate crash: %v", err)
	}
	if got := countNonTerminal(ctx, t, pool); got != 1 {
		t.Fatalf("after crash: %d non-terminal, want 1 stranded at DEBITED", got)
	}

	// RESTART: recovery re-enqueues, the poller drains to terminal. Loop bounded so
	// the multi-step chain (debited -> reversal_pending -> reversed) fully resolves.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := transfer.NewRecoverer(store).Recover(ctx); err != nil {
			t.Fatalf("recover: %v", err)
		}
		if err := outbox.NewPoller(store.Queries(), driver, outbox.Config{Batch: 16}).Drain(ctx); err != nil {
			t.Fatalf("drain: %v", err)
		}
		if countNonTerminal(ctx, t, pool) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("transfer still non-terminal after recovery deadline")
		}
		time.Sleep(20 * time.Millisecond)
	}

	got, err := svc.Get(ctx, view.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != transfer.StateReversed {
		t.Fatalf("recovered state = %s, want REVERSED", got.State)
	}

	// No stranded debit: source restored, destination untouched, suspense drained.
	assertBalance(ctx, t, ledgerClient, srcAccount, 0)
	assertBalance(ctx, t, ledgerClient, dstAccount, 0)
	assertBalance(ctx, t, ledgerClient, suspenseAcct, 0)
}
