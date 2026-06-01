package reconcile_test

import (
	"io"
	"testing"
	"time"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/reconcile"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/reconcile/fixture"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
)

// extStream is a Stream over an in-memory slice of external records. It mirrors
// what an adapter feeds the matcher, without touching disk.
type extStream struct {
	recs []canonical.Record
	i    int
}

func (s *extStream) Next() (canonical.Record, error) {
	if s.i >= len(s.recs) {
		return canonical.Record{}, io.EOF
	}
	r := s.recs[s.i]
	s.i++
	return r, nil
}

// TestFixture_FullRecall asserts the matcher recovers every injected
// discrepancy with the correct category label (AC-3): 100% recall, no
// misclassification, and no spurious exceptions.
func TestFixture_FullRecall(t *testing.T) {
	f := fixture.Generate(fixture.Spec{Count: 50, PerCategory: 4, Seed: 42})

	res, err := reconcile.Match(f.Internal, &extStream{recs: f.External}, 2*time.Minute)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}

	// Matched count must be exactly the clean pairs (+ the first leg of each
	// duplicate).
	if res.MatchedCount != f.ExpectedMatched {
		t.Errorf("matched = %d, want %d", res.MatchedCount, f.ExpectedMatched)
	}

	// Index the produced exceptions by reference and tally by category.
	gotCategory := make(map[string]reconcile.Category)
	gotByCategory := make(map[reconcile.Category]int)
	for _, e := range res.Exceptions {
		gotByCategory[e.Category]++
		gotCategory[e.Reference] = e.Category
	}

	// 100% recall: every injected discrepancy is found with the right label.
	for ref, wantCat := range f.ExpectedByRef {
		got, ok := gotCategory[ref]
		if !ok {
			t.Errorf("missed discrepancy %s (expected %s)", ref, wantCat)
			continue
		}
		if got != wantCat {
			t.Errorf("discrepancy %s labeled %s, want %s", ref, got, wantCat)
		}
	}

	// Per-category counts match, so there are no spurious exceptions either.
	for cat, want := range f.ExpectedByCategory {
		if gotByCategory[cat] != want {
			t.Errorf("category %s = %d, want %d", cat, gotByCategory[cat], want)
		}
	}
	if len(res.Exceptions) != totalExpected(f) {
		t.Errorf("total exceptions = %d, want %d", len(res.Exceptions), totalExpected(f))
	}
}

// TestFixture_DeterministicReport asserts AC-4: re-running the same inputs yields
// a byte-identical report (no double-counting, order-independent).
func TestFixture_DeterministicReport(t *testing.T) {
	f := fixture.Generate(fixture.Spec{Count: 30, PerCategory: 3, Seed: 99})

	render := func() []byte {
		res, err := reconcile.Match(f.Internal, &extStream{recs: f.External}, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		b, err := reconcile.NewReport("in.jsonl", "ext.csv", res).JSON()
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	first := render()
	for i := 0; i < 4; i++ {
		if got := render(); string(got) != string(first) {
			t.Fatalf("report not deterministic on run %d", i+2)
		}
	}
}

func totalExpected(f fixture.Fixture) int {
	n := 0
	for _, c := range f.ExpectedByCategory {
		n += c
	}
	return n
}
