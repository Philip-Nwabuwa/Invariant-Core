package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/switch/outbox"
)

// Ledger is the switch's view of the ledger. Each leg carries a deterministic
// idempotency key (<transfer-id>:debit|settle), so re-driving a leg after a
// crash returns the existing posting instead of moving money twice.
type Ledger interface {
	// PostDebitLeg debits the source into settlement and returns the ledger
	// transaction id (the reversal parent).
	PostDebitLeg(ctx context.Context, t Transfer) (uuid.UUID, error)
	// PostSettlementLeg debits settlement and credits the destination.
	PostSettlementLeg(ctx context.Context, t Transfer) error
}

// RailVerdict is the rail's answer for a leg, as the driver sees it.
type RailVerdict int

const (
	// VerdictUnknown means the rail outcome is in doubt (timeout / no answer):
	// resolve with a TSQ before deciding, never assume success or failure.
	VerdictUnknown RailVerdict = iota
	// VerdictSuccess means the rail accepted and will settle the leg.
	VerdictSuccess
	// VerdictDeclined means the rail refused the leg.
	VerdictDeclined
)

// Rail is the switch's view of the payment rail. Send returns a verdict plus a
// transport error; a transport error is transient (retry), while VerdictUnknown
// is a definitive "we don't know" that routes to the in-doubt path.
type Rail interface {
	Send(ctx context.Context, t Transfer) (RailVerdict, error)
}

// Driver advances a transfer through the state machine in response to outbox
// events. It is the single place post-debit progression happens, so a crash
// simply re-delivers the event and the driver resumes from the persisted status.
type Driver struct {
	store  *PostgresStore
	ledger Ledger
	rail   Rail
}

// NewDriver builds a Driver over its dependencies.
func NewDriver(store *PostgresStore, ledger Ledger, rail Rail) *Driver {
	return &Driver{store: store, ledger: ledger, rail: rail}
}

// Driver implements outbox.Handler — verified at compile time.
var _ outbox.Handler = (*Driver)(nil)

// Handle dispatches an outbox event to the matching step. Handlers are
// idempotent: a duplicate delivery re-checks the persisted status and no-ops.
func (d *Driver) Handle(ctx context.Context, evt outbox.Event) error {
	switch evt.Type {
	case outbox.EventDebitRequested:
		return d.handleDebitRequested(ctx, evt.AggregateID)
	case outbox.EventDebited:
		return d.handleDebited(ctx, evt.AggregateID)
	default:
		return fmt.Errorf("driver: no handler for event %q", evt.Type)
	}
}

// handleDebitRequested posts the debit leg (idempotent by key) and advances the
// transfer to DEBITED, or fails it closed if the ledger rejects it terminally.
// Recovery relies on this reconciling by reference: the debit key is the
// transfer id, so a re-drive after a crash never double-debits.
func (d *Driver) handleDebitRequested(ctx context.Context, id uuid.UUID) error {
	det, err := d.store.load(ctx, id)
	if err != nil {
		return err
	}
	if det.Status != statusPending {
		return nil // already advanced
	}
	ledgerTxID, err := d.ledger.PostDebitLeg(ctx, det.transfer())
	if err != nil {
		if isTerminalLedgerError(err) {
			_, ferr := d.store.markFailed(ctx, id)
			return ferr
		}
		return fmt.Errorf("post debit leg: %w", err)
	}
	_, err = d.store.markDebited(ctx, id, ledgerTxID, det.eventPayload())
	return err
}

// handleDebited sends the transfer to the rail and routes on the verdict:
// success settles, decline reverses, unknown goes in-doubt (TSQ resolves it in
// NS-303). Re-delivery is safe: the rail is deterministic by reference and the
// settlement leg is idempotent, so a re-run produces the same outcome once.
func (d *Driver) handleDebited(ctx context.Context, id uuid.UUID) error {
	det, err := d.store.load(ctx, id)
	if err != nil {
		return err
	}
	if det.Status != statusDebited {
		return nil // already advanced
	}
	verdict, err := d.rail.Send(ctx, det.transfer())
	if err != nil {
		return fmt.Errorf("rail send: %w", err)
	}
	switch verdict {
	case VerdictSuccess:
		if err := d.ledger.PostSettlementLeg(ctx, det.transfer()); err != nil {
			return fmt.Errorf("post settlement leg: %w", err)
		}
		_, err := d.store.markSettled(ctx, id)
		return err
	case VerdictDeclined:
		_, err := d.store.markReversalPending(ctx, id, det.eventPayload())
		return err
	default: // VerdictUnknown
		_, err := d.store.markInDoubt(ctx, id, det.eventPayload())
		return err
	}
}

// transferEventPayload is the JSON body of a transfer outbox event. The handler
// re-loads the row for the authoritative status, so the payload is informational
// (and useful for debugging the outbox).
type transferEventPayload struct {
	Reference   string `json:"reference"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

// eventPayload renders the transfer's fields for an outbox event.
func (d transferDetail) eventPayload() []byte {
	b, _ := json.Marshal(transferEventPayload{
		Reference:   d.Reference,
		Source:      d.Source,
		Destination: d.Destination,
		AmountMinor: d.Amount.Minor(),
		Currency:    d.Currency,
	})
	return b
}

// isTerminalLedgerError reports whether err is a ledger rejection that retrying
// cannot fix (unknown account, invalid request) — as opposed to a transient
// failure. It unwraps wrapped gRPC status errors.
func isTerminalLedgerError(err error) bool {
	var se interface{ GRPCStatus() *status.Status }
	if errors.As(err, &se) {
		switch se.GRPCStatus().Code() {
		case codes.NotFound, codes.InvalidArgument:
			return true
		}
	}
	return false
}
