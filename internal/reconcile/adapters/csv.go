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

// CSVReader is a generic, header-driven settlement reader. Unlike the fixed
// NIBSS layout it maps columns by header name, so column order and extra columns
// are tolerated; only reference + amount_minor are required.
type CSVReader struct {
	r      *csv.Reader
	idx    map[string]int // header name (lower-case) → column index
	closer io.Closer
	row    int
}

// Recognized header names. Anything else in the file is ignored.
const (
	colReference   = "reference"
	colAmountMinor = "amount_minor"
	colCurrency    = "currency"
	colSource      = "source"
	colDestination = "destination"
	colInitiatedAt = "initiated_at"
	colStatus      = "status"
)

// NewCSVReader builds a reader over r, indexing the header row.
func NewCSVReader(r io.Reader) (*CSVReader, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // tolerate ragged extra columns
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read csv header: %w", err)
	}
	idx := make(map[string]int, len(header))
	for i, h := range header {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, required := range []string{colReference, colAmountMinor} {
		if _, ok := idx[required]; !ok {
			return nil, fmt.Errorf("%w: %q", ErrMissingColumn, required)
		}
	}
	return &CSVReader{r: cr, idx: idx, row: 1}, nil
}

// OpenCSVFile opens path as a generic settlement CSV.
func OpenCSVFile(path string) (*CSVReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open csv file %q: %w", path, err)
	}
	rd, err := NewCSVReader(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	rd.closer = f
	return rd, nil
}

// Next reads and maps the next row, returning io.EOF at end of file.
func (c *CSVReader) Next() (canonical.Record, error) {
	rec, err := c.r.Read()
	if err != nil {
		if err == io.EOF {
			return canonical.Record{}, io.EOF
		}
		return canonical.Record{}, rowErr(c.row+1, err)
	}
	c.row++

	get := func(col string) string {
		i, ok := c.idx[col]
		if !ok || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}

	amount, err := strconv.ParseInt(get(colAmountMinor), 10, 64)
	if err != nil {
		return canonical.Record{}, rowErr(c.row, fmt.Errorf("amount_minor: %w", err))
	}
	status, err := parseStatus(get(colStatus))
	if err != nil {
		return canonical.Record{}, rowErr(c.row, fmt.Errorf("status %q: %w", get(colStatus), err))
	}

	out := canonical.Record{
		Reference:   get(colReference),
		Source:      get(colSource),
		Destination: get(colDestination),
		AmountMinor: money.FromMinor(amount),
		Currency:    get(colCurrency),
		Type:        canonical.TypeTransfer,
		Status:      status,
	}
	if raw := get(colInitiatedAt); raw != "" {
		ts, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return canonical.Record{}, rowErr(c.row, fmt.Errorf("initiated_at: %w", err))
		}
		out.InitiatedAt = ts.UTC()
	}
	return out, nil
}

// Close releases the underlying file handle if this reader owns one.
func (c *CSVReader) Close() error {
	if c.closer != nil {
		return c.closer.Close()
	}
	return nil
}
