package reconcile

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// Report is the rendered outcome of a reconciliation pass. It deliberately omits
// any wall-clock time so that re-running identical inputs yields byte-identical
// output (AC-4). Everything here derives from the sorted Result.
type Report struct {
	InternalSource string         `json:"internal_source"`
	ExternalSource string         `json:"external_source"`
	MatchedCount   int            `json:"matched_count"`
	ExceptionCount int            `json:"exception_count"`
	ByCategory     map[string]int `json:"by_category"`
	Exceptions     []Exception    `json:"exceptions"`
}

// NewReport assembles a Report from a reconciliation Result.
func NewReport(internalSrc, externalSrc string, res Result) Report {
	byCat := make(map[string]int)
	for _, e := range res.Exceptions {
		byCat[string(e.Category)]++
	}
	return Report{
		InternalSource: internalSrc,
		ExternalSource: externalSrc,
		MatchedCount:   res.MatchedCount,
		ExceptionCount: len(res.Exceptions),
		ByCategory:     byCat,
		Exceptions:     res.Exceptions,
	}
}

// JSON renders the machine-readable report. encoding/json sorts map keys, and
// the exceptions are already sorted, so the bytes are stable across runs.
func (r Report) JSON() ([]byte, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal report: %w", err)
	}
	return b, nil
}

// Text renders the human-readable report.
func (r Report) Text() string {
	var b strings.Builder
	fmt.Fprintln(&b, "Reconciliation report")
	fmt.Fprintf(&b, "  internal:   %s\n", r.InternalSource)
	fmt.Fprintf(&b, "  external:   %s\n", r.ExternalSource)
	fmt.Fprintf(&b, "  matched:    %d\n", r.MatchedCount)
	fmt.Fprintf(&b, "  exceptions: %d\n", r.ExceptionCount)

	if len(r.ByCategory) > 0 {
		fmt.Fprintln(&b, "  by category:")
		cats := make([]string, 0, len(r.ByCategory))
		for c := range r.ByCategory {
			cats = append(cats, c)
		}
		sort.Strings(cats)
		for _, c := range cats {
			fmt.Fprintf(&b, "    %-20s %d\n", c, r.ByCategory[c])
		}
	}

	if len(r.Exceptions) > 0 {
		fmt.Fprintln(&b, "exceptions:")
		for _, e := range r.Exceptions {
			fmt.Fprintf(&b, "  [%s] ref=%s%s\n", e.Category, e.Reference, exceptionDetail(e))
		}
	}
	return b.String()
}

// exceptionDetail adds category-specific context to a text exception line.
func exceptionDetail(e Exception) string {
	switch e.Category {
	case CategoryAmountMismatch:
		var in, ex string
		if e.Internal != nil {
			in = money.FromMinor(e.Internal.AmountMinor.Minor()).String()
		}
		if e.External != nil {
			ex = money.FromMinor(e.External.AmountMinor.Minor()).String()
		}
		delta := int64(0)
		if e.DeltaMinor != nil {
			delta = *e.DeltaMinor
		}
		return fmt.Sprintf(" internal=%s external=%s delta=%+d", in, ex, delta)
	case CategoryPendingReversal, CategoryUnmatchedInternal:
		if e.Internal != nil {
			return fmt.Sprintf(" amount=%s status=%s", e.Internal.AmountMinor.String(), e.Internal.Status)
		}
	case CategoryUnmatchedExternal, CategoryDuplicate:
		if e.External != nil {
			return fmt.Sprintf(" amount=%s status=%s", e.External.AmountMinor.String(), e.External.Status)
		}
	}
	return ""
}
