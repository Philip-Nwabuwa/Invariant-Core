package transfer

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	ledgerv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/ledger/v1"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
)

// settlementAccount is the seeded suspense account every transfer passes
// through: the debit leg moves source -> SETTLEMENT, the settlement leg moves
// SETTLEMENT -> destination. Holding the money there between legs keeps each
// posting balanced on its own.
const settlementAccount = "SETTLEMENT"

// LedgerClient adapts the ledger gRPC client to the orchestrator's Ledger
// interface. Each leg is its own balanced two-entry ledger transaction carrying
// a deterministic idempotency key (<transfer-id>:debit|settle), so a re-driven
// leg returns the existing posting instead of moving money twice. Both carry the
// transfer's reference so reconcile can match them (ARCHITECTURE §3).
type LedgerClient struct {
	client ledgerv1.LedgerServiceClient
}

// NewLedgerClient wraps a generated LedgerServiceClient.
func NewLedgerClient(client ledgerv1.LedgerServiceClient) *LedgerClient {
	return &LedgerClient{client: client}
}

// LedgerClient implements Ledger — checked at compile time.
var _ Ledger = (*LedgerClient)(nil)

// PostDebitLeg debits the source and credits settlement, returning the ledger
// transaction id (the reversal parent).
func (l *LedgerClient) PostDebitLeg(ctx context.Context, t Transfer) (uuid.UUID, error) {
	return l.post(ctx, t, t.Source, settlementAccount, legKey(t.ID, "debit"), canonical.TypeTransfer, nil)
}

// PostSettlementLeg debits settlement and credits the destination.
func (l *LedgerClient) PostSettlementLeg(ctx context.Context, t Transfer) error {
	_, err := l.post(ctx, t, settlementAccount, t.Destination, legKey(t.ID, "settle"), canonical.TypeTransfer, nil)
	return err
}

// PostReversal posts the compensating transaction that restores the source:
// it debits settlement and credits the source, linked to the debit leg via
// parent_transaction_id (append-only — the journal is never edited). The
// <id>:reversal key plus the uq_reversal_per_parent index make a re-driven
// reversal an idempotent no-op in the ledger.
func (l *LedgerClient) PostReversal(ctx context.Context, t Transfer, parentLedgerTxID uuid.UUID) error {
	_, err := l.post(ctx, t, settlementAccount, t.Source, legKey(t.ID, "reversal"), canonical.TypeReversal, &parentLedgerTxID)
	return err
}

// legKey is the deterministic per-leg idempotency key.
func legKey(transferID uuid.UUID, leg string) string {
	return fmt.Sprintf("%s:%s", transferID, leg)
}

// post records one balanced transaction that debits debitAccount and credits
// creditAccount for the transfer's amount, under the given idempotency key.
func (l *LedgerClient) post(ctx context.Context, t Transfer, debitAccount, creditAccount, idempotencyKey string, txType canonical.Type, parentTxID *uuid.UUID) (uuid.UUID, error) {
	req := &ledgerv1.PostTransactionRequest{
		Reference:      t.Reference,
		Type:           string(txType),
		Status:         string(canonical.StatusSettled),
		IdempotencyKey: idempotencyKey,
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
	}
	if parentTxID != nil {
		req.ParentTransactionId = parentTxID.String()
	}
	resp, err := l.client.PostTransaction(ctx, req)
	if err != nil {
		return uuid.Nil, fmt.Errorf("ledger post %s->%s: %w", debitAccount, creditAccount, err)
	}
	id, err := uuid.Parse(resp.GetTransactionId())
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse ledger tx id %q: %w", resp.GetTransactionId(), err)
	}
	return id, nil
}
