//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/testsupport"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/mockrail"
	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// TestIdempotentReplay drives a transfer through the production IdempotentService
// decorator: the same Idempotency-Key + body returns the same transfer (one row),
// while the same key with an altered body is a conflict.
func TestIdempotentReplay(t *testing.T) {
	pool := testsupport.NewPool(t) // skips when Docker is unavailable
	ctx := context.Background()

	ledgerClient := dialLedger(t, pool)
	// An always-succeed rail (every probability zero) so the transfer settles.
	railClient := transfer.NewRailClient(dialRail(t, mockrail.NewServerWithConfig(mockrail.Config{Seed: 1})))
	svc, _, _ := newIdempotentSwitch(pool, transfer.NewLedgerClient(ledgerClient), railClient)

	const ref = "REF-INT-IDEMPOTENT"
	const key = "key-int-idem"
	req := transfer.CreateRequest{
		Source: srcAccount, Destination: dstAccount, Amount: money.FromMinor(5000), Currency: "NGN", Reference: ref,
	}

	first, err := svc.Create(ctx, key, req)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Replay: same key + body returns the same transfer, no second row.
	replay, err := svc.Create(ctx, key, req)
	if err != nil {
		t.Fatalf("replay create: %v", err)
	}
	if replay.ID != first.ID {
		t.Errorf("replay id = %s, want %s (same transfer)", replay.ID, first.ID)
	}
	if got := transferRowCount(ctx, t, pool, ref); got != 1 {
		t.Errorf("transfer rows = %d, want 1 (replay must not create a second)", got)
	}

	// Same key, altered body (different amount) is a conflict.
	altered := req
	altered.Amount = money.FromMinor(9999)
	if _, err := svc.Create(ctx, key, altered); !errors.Is(err, transfer.ErrIdempotencyConflict) {
		t.Errorf("altered-body err = %v, want ErrIdempotencyConflict", err)
	}
}
