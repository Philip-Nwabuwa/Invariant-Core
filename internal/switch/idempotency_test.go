package transfer_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/testsupport"
	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// sameJSON reports whether a and b are the same JSON value, ignoring formatting.
func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("unmarshal %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("unmarshal %s: %v", b, err)
	}
	return reflect.DeepEqual(av, bv)
}

func sampleRequest() transfer.CreateRequest {
	return transfer.CreateRequest{
		Source:      "CUST-001",
		Destination: "SETTLEMENT",
		Amount:      money.FromMinor(5000),
		Currency:    "NGN",
		Reference:   "ref-1",
	}
}

func TestFingerprint_StableAndSensitive(t *testing.T) {
	base := transfer.Fingerprint(sampleRequest())

	// Identical request → identical fingerprint.
	if got := transfer.Fingerprint(sampleRequest()); got != base {
		t.Errorf("fingerprint not stable: %q != %q", got, base)
	}

	// A changed amount → different fingerprint.
	changed := sampleRequest()
	changed.Amount = money.FromMinor(5001)
	if transfer.Fingerprint(changed) == base {
		t.Error("fingerprint did not change when amount changed")
	}
}

func TestIdempotencyStore_Outcomes(t *testing.T) {
	pool := testsupport.NewPool(t) // skips when Docker is unavailable
	store := transfer.NewIdempotencyStore(pool)
	ctx := context.Background()

	fp := transfer.Fingerprint(sampleRequest())

	// 1. First reserve of a fresh key → Reserved.
	res, err := store.Reserve(ctx, "key-A", fp)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if res.Outcome != transfer.OutcomeReserved {
		t.Fatalf("first reserve outcome = %v, want Reserved", res.Outcome)
	}

	// 2. Same key while still in_progress → InProgress.
	res, err = store.Reserve(ctx, "key-A", fp)
	if err != nil {
		t.Fatalf("reserve (in progress): %v", err)
	}
	if res.Outcome != transfer.OutcomeInProgress {
		t.Fatalf("second reserve outcome = %v, want InProgress", res.Outcome)
	}

	// 3. Complete the key, then replay → Replay with the stored response.
	stored := []byte(`{"id":"abc","state":"SETTLED"}`)
	if err := store.Complete(ctx, "key-A", transfer.IdemSucceeded, nil, stored); err != nil {
		t.Fatalf("complete: %v", err)
	}
	res, err = store.Reserve(ctx, "key-A", fp)
	if err != nil {
		t.Fatalf("reserve (replay): %v", err)
	}
	if res.Outcome != transfer.OutcomeReplay {
		t.Fatalf("replay outcome = %v, want Replay", res.Outcome)
	}
	// JSONB stores parsed JSON, not the original bytes, so it round-trips with
	// different whitespace. Compare semantically, not byte-for-byte.
	if !sameJSON(t, res.Response, stored) {
		t.Errorf("replay response = %s, want (semantically) %s", res.Response, stored)
	}

	// 4. Same key, different fingerprint → Conflict.
	res, err = store.Reserve(ctx, "key-A", "a-different-fingerprint")
	if err != nil {
		t.Fatalf("reserve (conflict): %v", err)
	}
	if res.Outcome != transfer.OutcomeConflict {
		t.Fatalf("conflict outcome = %v, want Conflict", res.Outcome)
	}
}
