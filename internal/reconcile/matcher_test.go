package reconcile

import (
	"io"
	"testing"
	"time"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// sliceStream is a test Stream over an in-memory slice.
type sliceStream struct {
	recs []canonical.Record
	i    int
}

func (s *sliceStream) Next() (canonical.Record, error) {
	if s.i >= len(s.recs) {
		return canonical.Record{}, io.EOF
	}
	r := s.recs[s.i]
	s.i++
	return r, nil
}

var baseTime = time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)

func transfer(ref string, minor int64, status canonical.Status) canonical.Record {
	return canonical.Record{
		Reference:   ref,
		Source:      "CUST-001",
		Destination: "CUST-002",
		AmountMinor: money.FromMinor(minor),
		Currency:    "NGN",
		Type:        canonical.TypeTransfer,
		Status:      status,
		InitiatedAt: baseTime,
	}
}

func reversal(ref string, status canonical.Status) canonical.Record {
	r := transfer(ref, 5000, status)
	r.Type = canonical.TypeReversal
	return r
}

// countByCategory tallies exceptions by category.
func countByCategory(ex []Exception) map[Category]int {
	m := make(map[Category]int)
	for _, e := range ex {
		m[e.Category]++
	}
	return m
}

func TestMatch_HappyPath(t *testing.T) {
	internal := []canonical.Record{
		transfer("R1", 5000, canonical.StatusSettled),
		transfer("R2", 250, canonical.StatusSettled),
	}
	external := &sliceStream{recs: []canonical.Record{
		transfer("R1", 5000, canonical.StatusSettled),
		transfer("R2", 250, canonical.StatusSettled),
	}}
	res, err := Match(internal, external, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if res.MatchedCount != 2 {
		t.Errorf("matched = %d, want 2", res.MatchedCount)
	}
	if len(res.Exceptions) != 0 {
		t.Errorf("exceptions = %v, want none", res.Exceptions)
	}
}

func TestMatch_EveryCategory(t *testing.T) {
	internal := []canonical.Record{
		transfer("R1", 5000, canonical.StatusSettled),  // matched
		transfer("R2", 5000, canonical.StatusSettled),  // amount_mismatch
		transfer("R3", 5000, canonical.StatusSettled),  // unmatched_internal
		transfer("R4", 5000, canonical.StatusFailed),   // pending_reversal (no reversal)
		transfer("R5", 5000, canonical.StatusTimedOut), // resolved by settled reversal → unmatched_internal
		reversal("R5", canonical.StatusSettled),
	}
	external := &sliceStream{recs: []canonical.Record{
		transfer("R1", 5000, canonical.StatusSettled), // match
		transfer("R2", 6000, canonical.StatusSettled), // amount mismatch (delta +1000)
		transfer("R6", 5000, canonical.StatusSettled), // unmatched_external
		transfer("R1", 5000, canonical.StatusSettled), // duplicate of R1
	}}

	res, err := Match(internal, external, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if res.MatchedCount != 1 {
		t.Errorf("matched = %d, want 1", res.MatchedCount)
	}
	got := countByCategory(res.Exceptions)
	want := map[Category]int{
		CategoryAmountMismatch:    1,
		CategoryUnmatchedExternal: 1,
		CategoryDuplicate:         1,
		CategoryPendingReversal:   1,
		CategoryUnmatchedInternal: 2, // R3 and R5 (R5 reversed but still unmatched externally)
	}
	for cat, n := range want {
		if got[cat] != n {
			t.Errorf("category %s = %d, want %d (all: %v)", cat, got[cat], n, got)
		}
	}

	// Verify the amount-mismatch delta is external − internal.
	for _, e := range res.Exceptions {
		if e.Category == CategoryAmountMismatch {
			if e.DeltaMinor == nil || *e.DeltaMinor != 1000 {
				t.Errorf("amount_mismatch delta = %v, want 1000", e.DeltaMinor)
			}
		}
	}
}

func TestMatch_DeterministicOrder(t *testing.T) {
	internal := []canonical.Record{
		transfer("Z", 5000, canonical.StatusSettled),
		transfer("A", 5000, canonical.StatusSettled),
		transfer("M", 5000, canonical.StatusSettled),
	}
	// All unmatched externally; iterate twice with different map states implicitly.
	run := func() []string {
		res, err := Match(internal, &sliceStream{}, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		var refs []string
		for _, e := range res.Exceptions {
			refs = append(refs, string(e.Category)+":"+e.Reference)
		}
		return refs
	}
	first := run()
	for i := 0; i < 5; i++ {
		got := run()
		if len(got) != len(first) {
			t.Fatalf("length drift: %v vs %v", got, first)
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("order not deterministic: %v vs %v", got, first)
			}
		}
	}
}

func TestMatch_WindowViolationIsExceptional(t *testing.T) {
	internal := []canonical.Record{transfer("R1", 5000, canonical.StatusSettled)}
	far := transfer("R1", 5000, canonical.StatusSettled)
	far.InitiatedAt = baseTime.Add(10 * time.Minute)
	res, err := Match(internal, &sliceStream{recs: []canonical.Record{far}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if res.MatchedCount != 0 {
		t.Errorf("matched = %d, want 0 (outside window)", res.MatchedCount)
	}
	if len(res.Exceptions) != 1 || res.Exceptions[0].Category != CategoryAmountMismatch {
		t.Errorf("exceptions = %v, want one amount_mismatch", res.Exceptions)
	}
}
