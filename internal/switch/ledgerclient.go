package transfer

import (
	"context"
	"fmt"

	ledgerv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/ledger/v1"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
)

// settlementAccount is the seeded suspense account every transfer passes
// through: the debit leg moves source -> SETTLEMENT, the settlement leg moves
// SETTLEMENT -> destination. Holding the money there between legs keeps each
// posting balanced on its own.
const settlementAccount = "SETTLEMENT"

// LedgerClient adapts the ledger gRPC client to the orchestrator's Ledger
// interface. Each leg is its own balanced two-entry ledger transaction; both
// carry the transfer's reference so reconcile can match them (ARCHITECTURE §3).
// cmd/switchd dials the ledger and constructs one.
type LedgerClient struct {
	client ledgerv1.LedgerServiceClient
}

// NewLedgerClient wraps a generated LedgerServiceClient.
func NewLedgerClient(client ledgerv1.LedgerServiceClient) *LedgerClient {
	return &LedgerClient{client: client}
}

// LedgerClient implements Ledger — checked at compile time.
var _ Ledger = (*LedgerClient)(nil)

// PostDebitLeg debits the source and credits the settlement account.
func (l *LedgerClient) PostDebitLeg(ctx context.Context, t Transfer) error {
	return l.post(ctx, t, t.Source, settlementAccount)
}

// PostSettlementLeg debits the settlement account and credits the destination.
func (l *LedgerClient) PostSettlementLeg(ctx context.Context, t Transfer) error {
	return l.post(ctx, t, settlementAccount, t.Destination)
}

// post records one balanced transaction that debits debitAccount and credits
// creditAccount for the transfer's amount.
func (l *LedgerClient) post(ctx context.Context, t Transfer, debitAccount, creditAccount string) error {
	_, err := l.client.PostTransaction(ctx, &ledgerv1.PostTransactionRequest{
		Reference: t.Reference,
		Type:      string(canonical.TypeTransfer),
		Status:    string(canonical.StatusSettled),
		Entries: []*ledgerv1.EntryInput{
			{
				AccountCode: debitAccount,
				Direction:   ledgerv1.Direction_DIRECTION_DEBIT,
				AmountMinor: t.Amount.Minor(),
				Currency:    t.Currency,
			},
			{
				AccountCode: creditAccount,
				Direction:   ledgerv1.Direction_DIRECTION_CREDIT,
				AmountMinor: t.Amount.Minor(),
				Currency:    t.Currency,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("ledger post %s->%s: %w", debitAccount, creditAccount, err)
	}
	return nil
}
