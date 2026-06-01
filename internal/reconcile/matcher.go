package reconcile

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
)

// Stream is the streaming source of external settlement records. Adapters in
// internal/reconcile/adapters satisfy it structurally (Next returns io.EOF at
// end). The matcher never holds the whole external file — only the keyed index
// built from the internal side (FR-C7).
type Stream interface {
	Next() (canonical.Record, error)
}

// Result is the outcome of a reconciliation pass: how many records matched and
// the categorized gaps. Exceptions are sorted deterministically (NS-406).
type Result struct {
	MatchedCount int
	Exceptions   []Exception
}

// indexEntry is an internal transfer awaiting an external counterpart.
type indexEntry struct {
	rec     canonical.Record
	matched bool
}

// Match reconciles the internal ledger export against a stream of external
// settlement records, keying on canonical Reference. The internal side is
// indexed in memory; the external side is streamed. A match requires the same
// reference, exact amount + currency, and (when both timestamps are present)
// initiated_at within window (FR-C2).
func Match(internal []canonical.Record, external Stream, window time.Duration) (Result, error) {
	index := make(map[string]*indexEntry, len(internal))
	settledReversals := make(map[string]bool)
	for _, rec := range internal {
		if rec.Type == canonical.TypeReversal {
			// A reversal that has posted restores the source; it is not matched
			// against the settlement file, only used to resolve pending reversals.
			if rec.Status == canonical.StatusSettled || rec.Status == canonical.StatusReversed {
				settledReversals[rec.Reference] = true
			}
			continue
		}
		index[rec.Reference] = &indexEntry{rec: rec}
	}

	var exceptions []Exception
	seenExternal := make(map[string]bool)
	matched := 0

	for {
		ext, err := external.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Result{}, fmt.Errorf("read external record: %w", err)
		}

		extCopy := ext
		if seenExternal[ext.Reference] {
			// The same reference settled more than once externally.
			exceptions = append(exceptions, Exception{
				Category:  CategoryDuplicate,
				Reference: ext.Reference,
				External:  &extCopy,
			})
			continue
		}
		seenExternal[ext.Reference] = true

		entry, ok := index[ext.Reference]
		if !ok {
			exceptions = append(exceptions, Exception{
				Category:  CategoryUnmatchedExternal,
				Reference: ext.Reference,
				External:  &extCopy,
			})
			continue
		}

		intCopy := entry.rec
		entry.matched = true
		if ext.AmountMinor != entry.rec.AmountMinor || ext.Currency != entry.rec.Currency ||
			!withinWindow(entry.rec.InitiatedAt, ext.InitiatedAt, window) {
			delta := ext.AmountMinor.Minor() - entry.rec.AmountMinor.Minor()
			exceptions = append(exceptions, Exception{
				Category:   CategoryAmountMismatch,
				Reference:  ext.Reference,
				Internal:   &intCopy,
				External:   &extCopy,
				DeltaMinor: &delta,
			})
			continue
		}
		matched++
	}

	// Internal records with no external counterpart.
	for _, entry := range index {
		if entry.matched {
			continue
		}
		rec := entry.rec
		exceptions = append(exceptions, Exception{
			Category:  classifyUnmatchedInternal(rec, settledReversals),
			Reference: rec.Reference,
			Internal:  &rec,
		})
	}

	sortExceptions(exceptions)
	return Result{MatchedCount: matched, Exceptions: exceptions}, nil
}

// withinWindow reports whether two timestamps are within tolerance of each other.
// A zero timestamp on either side (e.g. an external file without a usable time)
// or a non-positive window disables the check.
func withinWindow(a, b time.Time, window time.Duration) bool {
	if window <= 0 || a.IsZero() || b.IsZero() {
		return true
	}
	d := a.Sub(b)
	if d < 0 {
		d = -d
	}
	return d <= window
}

// sortExceptions orders exceptions deterministically so the report is independent
// of input row order and map iteration (NS-406, AC-4).
func sortExceptions(ex []Exception) {
	sort.SliceStable(ex, func(i, j int) bool {
		if ex[i].Category != ex[j].Category {
			return ex[i].Category < ex[j].Category
		}
		if ex[i].Reference != ex[j].Reference {
			return ex[i].Reference < ex[j].Reference
		}
		return deltaKey(ex[i]) < deltaKey(ex[j])
	})
}

func deltaKey(e Exception) int64 {
	if e.DeltaMinor != nil {
		return *e.DeltaMinor
	}
	return 0
}
