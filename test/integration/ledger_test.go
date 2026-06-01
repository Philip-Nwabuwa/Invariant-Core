//go:build integration

package integration_test

import (
	"context"
	"testing"

	ledgerv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/ledger/v1"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/testsupport"
)

// TestSerializablePosting posts a balanced transfer through the real ledger over
// gRPC (a SERIALIZABLE write) and asserts the derived balances match the
// hand-computed figures: a 5000 kobo move from source to destination.
func TestSerializablePosting(t *testing.T) {
	pool := testsupport.NewPool(t) // skips when Docker is unavailable
	ctx := context.Background()
	client := dialLedger(t, pool)

	const amount = 5000
	_, err := client.PostTransaction(ctx, &ledgerv1.PostTransactionRequest{
		Reference: "REF-INT-LEDGER",
		Type:      "transfer",
		Status:    "settled",
		Entries: []*ledgerv1.EntryInput{
			{AccountCode: srcAccount, Direction: ledgerv1.Direction_DIRECTION_DEBIT, AmountMinor: amount, Currency: "NGN"},
			{AccountCode: dstAccount, Direction: ledgerv1.Direction_DIRECTION_CREDIT, AmountMinor: amount, Currency: "NGN"},
		},
	})
	if err != nil {
		t.Fatalf("post transaction: %v", err)
	}

	// Both accounts are asset (debit-normal): the debited source is +5000, the
	// credited destination is -5000, and the two net to zero (value conserved).
	assertBalance(ctx, t, client, srcAccount, amount)
	assertBalance(ctx, t, client, dstAccount, -amount)

	// An unbalanced post is rejected by the ledger (no partial write).
	_, err = client.PostTransaction(ctx, &ledgerv1.PostTransactionRequest{
		Reference: "REF-INT-UNBALANCED",
		Type:      "transfer",
		Entries: []*ledgerv1.EntryInput{
			{AccountCode: srcAccount, Direction: ledgerv1.Direction_DIRECTION_DEBIT, AmountMinor: amount, Currency: "NGN"},
			{AccountCode: dstAccount, Direction: ledgerv1.Direction_DIRECTION_CREDIT, AmountMinor: amount - 1, Currency: "NGN"},
		},
	})
	if err == nil {
		t.Fatal("unbalanced post succeeded, want rejection")
	}
}
