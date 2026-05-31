// Package adapters normalizes every reconciliation input — the internal ledger
// export and each external settlement format — into canonical.Record (ADR-0005,
// FR-C1). Adapters are the only place that knows about messy external layouts;
// nothing downstream of an adapter ever sees a raw row.
//
// Every adapter is a streaming iterator: Next returns one record at a time and
// io.EOF when the input is exhausted, so a large file never has to be held in
// memory (FR-C7).
package adapters

import (
	"errors"
	"fmt"
)

// Malformed-input errors. The CLI surfaces these with the offending source; the
// reconcile prefix keeps the mapping in one place (errors.Is).
var (
	// ErrMalformedRow signals a row that could not be parsed into a record.
	ErrMalformedRow = errors.New("reconcile: malformed row")
	// ErrMissingColumn signals a required column was absent from a CSV header.
	ErrMissingColumn = errors.New("reconcile: missing required column")
	// ErrUnknownStatus signals a status token the adapter does not recognize.
	ErrUnknownStatus = errors.New("reconcile: unknown status")
)

// rowErr wraps a parse failure with the 1-based row number for a useful message.
func rowErr(row int, err error) error {
	return fmt.Errorf("%w: row %d: %v", ErrMalformedRow, row, err)
}
