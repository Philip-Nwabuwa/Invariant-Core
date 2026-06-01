package adapters

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// NIBSS settlement files have a fixed column layout. The header row is required
// and validated so a shifted file fails loudly rather than mis-mapping columns.
var nibssColumns = []string{
	"session_id",          // cross-system reference (NIP session id)
	"source_account",      // debited account
	"beneficiary_account", // credited account
	"amount_kobo",         // integer minor units
	"currency",
	"status",
	"transaction_date", // RFC3339
}

// NIBSSReader streams a NIBSS-style settlement CSV into canonical records.
type NIBSSReader struct {
	r      *csv.Reader
	closer io.Closer
	row    int
}

// NewNIBSSReader builds a reader over r and validates the header row.
func NewNIBSSReader(r io.Reader) (*NIBSSReader, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = len(nibssColumns)
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read nibss header: %w", err)
	}
	if err := checkHeader(header, nibssColumns); err != nil {
		return nil, err
	}
	return &NIBSSReader{r: cr, row: 1}, nil
}

// OpenNIBSSFile opens path as a NIBSS settlement CSV.
func OpenNIBSSFile(path string) (*NIBSSReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open nibss file %q: %w", path, err)
	}
	rd, err := NewNIBSSReader(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	rd.closer = f
	return rd, nil
}

// Next reads and maps the next settlement row, returning io.EOF at end of file.
func (n *NIBSSReader) Next() (canonical.Record, error) {
	rec, err := n.r.Read()
	if err != nil {
		if err == io.EOF {
			return canonical.Record{}, io.EOF
		}
		return canonical.Record{}, rowErr(n.row+1, err)
	}
	n.row++

	amount, err := strconv.ParseInt(strings.TrimSpace(rec[3]), 10, 64)
	if err != nil {
		return canonical.Record{}, rowErr(n.row, fmt.Errorf("amount_kobo: %w", err))
	}
	status, err := parseStatus(rec[5])
	if err != nil {
		return canonical.Record{}, rowErr(n.row, fmt.Errorf("status %q: %w", rec[5], err))
	}
	ts, err := time.Parse(time.RFC3339, strings.TrimSpace(rec[6]))
	if err != nil {
		return canonical.Record{}, rowErr(n.row, fmt.Errorf("transaction_date: %w", err))
	}

	return canonical.Record{
		Reference:   strings.TrimSpace(rec[0]),
		Source:      strings.TrimSpace(rec[1]),
		Destination: strings.TrimSpace(rec[2]),
		AmountMinor: money.FromMinor(amount),
		Currency:    strings.TrimSpace(rec[4]),
		Type:        canonical.TypeTransfer,
		Status:      status,
		InitiatedAt: ts.UTC(),
	}, nil
}

// Close releases the underlying file handle if this reader owns one.
func (n *NIBSSReader) Close() error {
	if n.closer != nil {
		return n.closer.Close()
	}
	return nil
}

// checkHeader confirms the header row matches the expected columns (order and
// names), trimming and lower-casing for tolerance.
func checkHeader(header, want []string) error {
	if len(header) != len(want) {
		return fmt.Errorf("%w: header has %d columns, want %d", ErrMalformedRow, len(header), len(want))
	}
	for i, col := range want {
		if !strings.EqualFold(strings.TrimSpace(header[i]), col) {
			return fmt.Errorf("%w: column %d is %q, want %q", ErrMissingColumn, i, header[i], col)
		}
	}
	return nil
}
