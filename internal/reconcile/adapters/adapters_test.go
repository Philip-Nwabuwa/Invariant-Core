package adapters

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
)

// drain reads every record from a stream into a slice.
func drain(t *testing.T, next func() (canonical.Record, error)) []canonical.Record {
	t.Helper()
	var out []canonical.Record
	for {
		rec, err := next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		out = append(out, rec)
	}
}

func TestLedgerReader_JSONL(t *testing.T) {
	const jsonl = `{"reference":"R1","source":"A","destination":"B","amount_minor":5000,"currency":"NGN","type":"transfer","status":"settled","initiated_at":"2026-05-31T10:00:00Z"}
{"reference":"R2","source":"C","destination":"D","amount_minor":250,"currency":"NGN","type":"transfer","status":"failed","initiated_at":"2026-05-31T10:05:00Z"}
`
	recs := drain(t, NewLedgerReader(strings.NewReader(jsonl)).Next)
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	if recs[0].Reference != "R1" || recs[0].AmountMinor.Minor() != 5000 {
		t.Errorf("record 0 = %+v", recs[0])
	}
	if recs[1].Status != canonical.StatusFailed {
		t.Errorf("record 1 status = %q, want failed", recs[1].Status)
	}
}

func TestNIBSSReader_MapsCleanly(t *testing.T) {
	const data = `session_id,source_account,beneficiary_account,amount_kobo,currency,status,transaction_date
NIP-001, CUST-001 ,CUST-002,5000,NGN,00,2026-05-31T10:00:00Z
NIP-002,CUST-003,CUST-004,750,NGN,FAILED,2026-05-31T10:05:00Z
`
	recs := drain(t, mustNIBSS(t, data).Next)
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	want := canonical.Record{
		Reference:   "NIP-001",
		Source:      "CUST-001", // whitespace trimmed
		Destination: "CUST-002",
		Currency:    "NGN",
		Type:        canonical.TypeTransfer,
		Status:      canonical.StatusSettled, // "00" → settled
	}
	got := recs[0]
	if got.Reference != want.Reference || got.Source != want.Source ||
		got.Destination != want.Destination || got.Status != want.Status ||
		got.AmountMinor.Minor() != 5000 {
		t.Errorf("record 0 = %+v", got)
	}
	if recs[1].Status != canonical.StatusFailed {
		t.Errorf("record 1 status = %q, want failed", recs[1].Status)
	}
}

func TestNIBSSReader_RejectsBadHeader(t *testing.T) {
	const data = `wrong,header,row
a,b,c
`
	if _, err := NewNIBSSReader(strings.NewReader(data)); err == nil {
		t.Fatal("expected header validation error, got nil")
	}
}

func TestNIBSSReader_MalformedAmount(t *testing.T) {
	const data = `session_id,source_account,beneficiary_account,amount_kobo,currency,status,transaction_date
NIP-001,CUST-001,CUST-002,notanumber,NGN,00,2026-05-31T10:00:00Z
`
	rd := mustNIBSS(t, data)
	if _, err := rd.Next(); !errors.Is(err, ErrMalformedRow) {
		t.Fatalf("got %v, want ErrMalformedRow", err)
	}
}

func TestCSVReader_ToleratesOrderAndExtraColumns(t *testing.T) {
	// Columns reordered, an extra unknown column present, initiated_at omitted.
	const data = `note,amount_minor,reference,currency,status
ignore-me,5000,R1,NGN,settled
also-ignore,250,R2,NGN,
`
	recs := drain(t, mustCSV(t, data).Next)
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	if recs[0].Reference != "R1" || recs[0].AmountMinor.Minor() != 5000 {
		t.Errorf("record 0 = %+v", recs[0])
	}
	// Empty status token defaults to settled.
	if recs[1].Status != canonical.StatusSettled {
		t.Errorf("record 1 status = %q, want settled (empty default)", recs[1].Status)
	}
}

func TestCSVReader_RequiresReferenceAndAmount(t *testing.T) {
	const data = `currency,status
NGN,settled
`
	if _, err := NewCSVReader(strings.NewReader(data)); !errors.Is(err, ErrMissingColumn) {
		t.Fatalf("got %v, want ErrMissingColumn", err)
	}
}

func TestParseStatus(t *testing.T) {
	cases := map[string]canonical.Status{
		"00":      canonical.StatusSettled,
		"success": canonical.StatusSettled,
		"FAILED":  canonical.StatusFailed,
		"09":      canonical.StatusFailed,
		"pending": canonical.StatusPending,
		"":        canonical.StatusSettled,
	}
	for token, want := range cases {
		got, err := parseStatus(token)
		if err != nil {
			t.Errorf("parseStatus(%q): %v", token, err)
			continue
		}
		if got != want {
			t.Errorf("parseStatus(%q) = %q, want %q", token, got, want)
		}
	}
	if _, err := parseStatus("nonsense"); !errors.Is(err, ErrUnknownStatus) {
		t.Errorf("parseStatus(nonsense) = %v, want ErrUnknownStatus", err)
	}
}

func mustNIBSS(t *testing.T, data string) *NIBSSReader {
	t.Helper()
	rd, err := NewNIBSSReader(strings.NewReader(data))
	if err != nil {
		t.Fatalf("NewNIBSSReader: %v", err)
	}
	return rd
}

func mustCSV(t *testing.T, data string) *CSVReader {
	t.Helper()
	rd, err := NewCSVReader(strings.NewReader(data))
	if err != nil {
		t.Fatalf("NewCSVReader: %v", err)
	}
	return rd
}
