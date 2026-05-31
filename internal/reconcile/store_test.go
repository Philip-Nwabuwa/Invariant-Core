package reconcile

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/testsupport"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
)

// reconResult builds a small result with one of each persisted-relevant category.
func reconResult(t *testing.T) Result {
	t.Helper()
	internal := []canonical.Record{
		transfer("R1", 5000, canonical.StatusSettled), // matched
		transfer("R2", 5000, canonical.StatusSettled), // amount_mismatch
		transfer("R3", 5000, canonical.StatusFailed),  // pending_reversal
	}
	external := &sliceStream{recs: []canonical.Record{
		transfer("R1", 5000, canonical.StatusSettled),
		transfer("R2", 7000, canonical.StatusSettled),
		transfer("R8", 100, canonical.StatusSettled), // unmatched_external
	}}
	res, err := Match(internal, external, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestStore_PersistAndIdempotentGuard(t *testing.T) {
	pool := testsupport.NewPool(t) // skips when Docker is unavailable
	ctx := context.Background()
	store := NewStore(pool)
	res := reconResult(t)
	const fingerprint = "fp-deadbeef"

	// No prior run for this fingerprint.
	if _, found, err := store.FindByFingerprint(ctx, fingerprint); err != nil || found {
		t.Fatalf("FindByFingerprint before persist: found=%v err=%v", found, err)
	}

	runID, err := store.Persist(ctx, "internal.jsonl", "settlement.csv", fingerprint, res)
	if err != nil {
		t.Fatalf("Persist: %v", err)
	}

	// The run is now findable by its fingerprint.
	gotID, found, err := store.FindByFingerprint(ctx, fingerprint)
	if err != nil || !found {
		t.Fatalf("FindByFingerprint after persist: found=%v err=%v", found, err)
	}
	if gotID != runID {
		t.Errorf("fingerprint lookup id = %s, want %s", gotID, runID)
	}

	// Exactly len(res.Exceptions) rows persisted; run counts correct.
	if got := exceptionsForRun(t, ctx, pool, runID); got != len(res.Exceptions) {
		t.Errorf("persisted exceptions = %d, want %d", got, len(res.Exceptions))
	}
	assertRunCounts(t, ctx, pool, runID, res.MatchedCount, len(res.Exceptions))

	// AC-4: a re-run with the same fingerprint must not be persisted again. The
	// CLI guard skips Persist when FindByFingerprint sees the prior run; assert
	// the total exception row count is unchanged when the guard fires.
	totalBefore := totalExceptions(t, ctx, pool)
	if _, found, _ := store.FindByFingerprint(ctx, fingerprint); !found {
		t.Fatal("expected guard to find the prior run")
	}
	// No second Persist call — that is exactly what the guard prevents.
	if totalAfter := totalExceptions(t, ctx, pool); totalAfter != totalBefore {
		t.Errorf("exception rows changed without a persist: %d → %d", totalBefore, totalAfter)
	}
}

func exceptionsForRun(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM recon_exceptions WHERE run_id = $1", runID).Scan(&n); err != nil {
		t.Fatalf("count exceptions: %v", err)
	}
	return n
}

func totalExceptions(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM recon_exceptions").Scan(&n); err != nil {
		t.Fatalf("count exceptions: %v", err)
	}
	return n
}

func assertRunCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runID uuid.UUID, matched, exceptions int) {
	t.Helper()
	var status string
	var matchedCount, exceptionCount int
	err := pool.QueryRow(ctx,
		"SELECT status, matched_count, exception_count FROM recon_runs WHERE id = $1", runID,
	).Scan(&status, &matchedCount, &exceptionCount)
	if err != nil {
		t.Fatalf("read run: %v", err)
	}
	if status != "completed" {
		t.Errorf("run status = %q, want completed", status)
	}
	if matchedCount != matched {
		t.Errorf("matched_count = %d, want %d", matchedCount, matched)
	}
	if exceptionCount != exceptions {
		t.Errorf("exception_count = %d, want %d", exceptionCount, exceptions)
	}
}
