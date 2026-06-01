// Package fixture deterministically generates a paired internal ledger export
// and external settlement file with a known set of injected discrepancies, one
// per reconciliation category. It backs both scripts/gen_settlement (NS-407) and
// the fixture test (NS-408): the test asserts the matcher recovers exactly the
// injected discrepancies (AC-3), and identical seeds yield identical files.
package fixture

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"time"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/reconcile"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// Spec controls fixture generation.
type Spec struct {
	// Count is the number of clean matched transfers (present identically on
	// both sides).
	Count int
	// PerCategory is how many discrepancies to inject for each of the five
	// categories.
	PerCategory int
	// Seed makes generation reproducible; the same seed yields identical files.
	Seed int64
}

// Fixture is a generated input pair plus the discrepancies that were injected.
type Fixture struct {
	Internal []canonical.Record
	External []canonical.Record
	// ExpectedByCategory is the exception count the matcher should report per
	// category.
	ExpectedByCategory map[reconcile.Category]int
	// ExpectedByRef maps each injected discrepancy's reference to its category,
	// so a test can assert 100% recall with correct labels.
	ExpectedByRef map[string]reconcile.Category
	// ExpectedMatched is the number of records that should match cleanly.
	ExpectedMatched int
}

var baseTime = time.Date(2026, 5, 31, 9, 0, 0, 0, time.UTC)

// matchOffset keeps a matched external timestamp within any sane tolerance
// window while still differing from the internal one.
const matchOffset = 30 * time.Second

// Generate builds a deterministic fixture from spec.
func Generate(spec Spec) Fixture {
	rng := rand.New(rand.NewSource(spec.Seed)) //nolint:gosec // deterministic test data, not crypto
	f := Fixture{
		ExpectedByCategory: make(map[reconcile.Category]int),
		ExpectedByRef:      make(map[string]reconcile.Category),
	}

	add := func(internal, external *canonical.Record) {
		if internal != nil {
			f.Internal = append(f.Internal, *internal)
		}
		if external != nil {
			f.External = append(f.External, *external)
		}
	}

	amount := func() money.Amount { return money.FromMinor(int64(100 + rng.Intn(999_900))) }
	ts := func(i int) time.Time { return baseTime.Add(time.Duration(i) * time.Minute) }

	// Clean matched pairs.
	for i := 0; i < spec.Count; i++ {
		ref := fmt.Sprintf("TXN-%06d", i)
		amt := amount()
		in := record(ref, amt, canonical.TypeTransfer, canonical.StatusSettled, ts(i))
		ex := in
		ex.InitiatedAt = in.InitiatedAt.Add(matchOffset)
		add(&in, &ex)
		f.ExpectedMatched++
	}

	// amount_mismatch: present on both sides, external amount differs.
	for j := 0; j < spec.PerCategory; j++ {
		ref := fmt.Sprintf("AMIS-%04d", j)
		amt := amount()
		in := record(ref, amt, canonical.TypeTransfer, canonical.StatusSettled, ts(j))
		ex := in
		ex.AmountMinor = amt.Add(money.FromMinor(int64(100 * (j + 1))))
		ex.InitiatedAt = in.InitiatedAt.Add(matchOffset)
		add(&in, &ex)
		f.ExpectedByCategory[reconcile.CategoryAmountMismatch]++
		f.ExpectedByRef[ref] = reconcile.CategoryAmountMismatch
	}

	// unmatched_internal: internal settled transfer with no external row.
	for j := 0; j < spec.PerCategory; j++ {
		ref := fmt.Sprintf("UINT-%04d", j)
		in := record(ref, amount(), canonical.TypeTransfer, canonical.StatusSettled, ts(j))
		add(&in, nil)
		f.ExpectedByCategory[reconcile.CategoryUnmatchedInternal]++
		f.ExpectedByRef[ref] = reconcile.CategoryUnmatchedInternal
	}

	// unmatched_external: external row with no internal record.
	for j := 0; j < spec.PerCategory; j++ {
		ref := fmt.Sprintf("UEXT-%04d", j)
		ex := record(ref, amount(), canonical.TypeTransfer, canonical.StatusSettled, ts(j))
		add(nil, &ex)
		f.ExpectedByCategory[reconcile.CategoryUnmatchedExternal]++
		f.ExpectedByRef[ref] = reconcile.CategoryUnmatchedExternal
	}

	// duplicate: a clean match whose external row appears twice.
	for j := 0; j < spec.PerCategory; j++ {
		ref := fmt.Sprintf("DUP-%04d", j)
		amt := amount()
		in := record(ref, amt, canonical.TypeTransfer, canonical.StatusSettled, ts(j))
		ex := in
		ex.InitiatedAt = in.InitiatedAt.Add(matchOffset)
		add(&in, &ex)
		dup := ex
		add(nil, &dup) // second external occurrence → duplicate
		f.ExpectedMatched++
		f.ExpectedByCategory[reconcile.CategoryDuplicate]++
		f.ExpectedByRef[ref] = reconcile.CategoryDuplicate
	}

	// pending_reversal: internal failed transfer, no external row, no reversal.
	for j := 0; j < spec.PerCategory; j++ {
		ref := fmt.Sprintf("PREV-%04d", j)
		in := record(ref, amount(), canonical.TypeTransfer, canonical.StatusFailed, ts(j))
		add(&in, nil)
		f.ExpectedByCategory[reconcile.CategoryPendingReversal]++
		f.ExpectedByRef[ref] = reconcile.CategoryPendingReversal
	}

	// Shuffle both sides so output order carries no signal — the matcher must be
	// order-independent (FR-C6). Deterministic under the seed.
	rng.Shuffle(len(f.Internal), func(a, b int) { f.Internal[a], f.Internal[b] = f.Internal[b], f.Internal[a] })
	rng.Shuffle(len(f.External), func(a, b int) { f.External[a], f.External[b] = f.External[b], f.External[a] })
	return f
}

func record(ref string, amt money.Amount, typ canonical.Type, status canonical.Status, t time.Time) canonical.Record {
	return canonical.Record{
		Reference:   ref,
		Source:      "CUST-001",
		Destination: "CUST-002",
		AmountMinor: amt,
		Currency:    "NGN",
		Type:        typ,
		Status:      status,
		InitiatedAt: t.UTC(),
	}
}

// WriteJSONL writes records as the internal ledger export (one JSON object per
// line), the shape the ledger adapter reads.
func WriteJSONL(w io.Writer, recs []canonical.Record) error {
	enc := json.NewEncoder(w)
	for i := range recs {
		if err := enc.Encode(recs[i]); err != nil {
			return fmt.Errorf("encode record: %w", err)
		}
	}
	return nil
}
