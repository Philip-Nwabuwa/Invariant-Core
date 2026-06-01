package adapters

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
)

// LedgerReader streams the internal ledger export. The export is JSONL: one
// canonical.Record per line, exactly the shape ledger's ExportTransactions
// produces. A json.Decoder reads successive values without buffering the file.
type LedgerReader struct {
	dec    *json.Decoder
	closer io.Closer
}

// NewLedgerReader builds a reader over r. Close is a no-op unless r is also an
// io.Closer that the caller wants this reader to own (use OpenLedgerFile).
func NewLedgerReader(r io.Reader) *LedgerReader {
	return &LedgerReader{dec: json.NewDecoder(r)}
}

// OpenLedgerFile opens path as a JSONL ledger export. The returned reader owns
// the file handle; Close releases it.
func OpenLedgerFile(path string) (*LedgerReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open ledger export %q: %w", path, err)
	}
	return &LedgerReader{dec: json.NewDecoder(f), closer: f}, nil
}

// Next decodes the next record, returning io.EOF when the stream is exhausted.
func (l *LedgerReader) Next() (canonical.Record, error) {
	var rec canonical.Record
	if err := l.dec.Decode(&rec); err != nil {
		if err == io.EOF {
			return canonical.Record{}, io.EOF
		}
		return canonical.Record{}, rowErr(0, err)
	}
	return rec, nil
}

// Close releases the underlying file handle if this reader owns one.
func (l *LedgerReader) Close() error {
	if l.closer != nil {
		return l.closer.Close()
	}
	return nil
}
