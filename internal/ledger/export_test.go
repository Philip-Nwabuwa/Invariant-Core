package ledger

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
)

// TestExportTransactions_JSONRoundTrip posts a transfer, exports it as a
// canonical.Record over a window, and asserts the record carries the journal
// facts and round-trips losslessly through JSON (FR-L5 / DoD #5).
func TestExportTransactions_JSONRoundTrip(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	before := time.Now().UTC().Add(-time.Minute)
	if _, err := svc.PostTransaction(ctx, PostRequest{
		Reference: "REF-EXP",
		Type:      canonical.TypeTransfer,
		Entries: []EntryInput{
			entry("CUST-001", Debit, 5000),
			entry("SETTLEMENT", Credit, 5000),
		},
	}); err != nil {
		t.Fatalf("post: %v", err)
	}
	after := time.Now().UTC().Add(time.Minute)

	recs, err := svc.ExportTransactions(ctx, before, after)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	rec := recs[0]
	if rec.Reference != "REF-EXP" || rec.AmountMinor.Minor() != 5000 {
		t.Fatalf("record fields: %+v", rec)
	}
	if rec.Type != canonical.TypeTransfer || rec.Status != canonical.StatusPending {
		t.Fatalf("type/status: %+v", rec)
	}
	// Debit destination, credit source (see ExportTransactions derivation).
	if rec.Destination != "CUST-001" || rec.Source != "SETTLEMENT" {
		t.Fatalf("source/destination: src=%q dst=%q", rec.Source, rec.Destination)
	}

	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back canonical.Record
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Reference != rec.Reference ||
		back.AmountMinor != rec.AmountMinor ||
		back.Type != rec.Type ||
		back.Status != rec.Status ||
		!back.InitiatedAt.Equal(rec.InitiatedAt) {
		t.Fatalf("JSON round trip diverged:\n got %+v\nwant %+v", back, rec)
	}
}
