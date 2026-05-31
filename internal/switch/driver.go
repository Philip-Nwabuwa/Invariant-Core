package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
	// PostReversal posts the compensating transaction (settlement -> source),
	// linked to the debit leg, restoring the source. Idempotent by leg key.
	PostReversal(ctx context.Context, t Transfer, parentLedgerTxID uuid.UUID) error
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
	// QueryStatus is a TSQ: it asks the rail whether the reference settled, so an
	// in-doubt transfer is resolved with a fact rather than a guess. A
	// VerdictUnknown (or error) means the rail could not say.
	QueryStatus(ctx context.Context, reference string) (RailVerdict, error)
}

// Driver advances a transfer through the state machine in response to outbox
// events. It is the single place post-debit progression happens, so a crash
// simply re-delivers the event and the driver resumes from the persisted status.
type Driver struct {
	store       *PostgresStore
	ledger      Ledger
	rail        Rail
	tsqAttempts int           // TSQ tries before holding for manual review
	tsqBackoff  time.Duration // wait between TSQ tries
}

// DriverOption customizes a Driver.
type DriverOption func(*Driver)

// WithTSQPolicy sets how persistently the in-doubt handler queries the rail
// before giving up and routing to MANUAL_REVIEW.
func WithTSQPolicy(attempts int, backoff time.Duration) DriverOption {
	return func(d *Driver) {
		d.tsqAttempts = attempts
		d.tsqBackoff = backoff
	}
}

// NewDriver builds a Driver over its dependencies.
func NewDriver(store *PostgresStore, ledger Ledger, rail Rail, opts ...DriverOption) *Driver {
	d := &Driver{store: store, ledger: ledger, rail: rail, tsqAttempts: 3, tsqBackoff: 500 * time.Millisecond}
	for _, o := range opts {
		o(d)
	}
	return d
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
	case outbox.EventInDoubt:
		return d.handleInDoubt(ctx, evt.AggregateID)
	case outbox.EventReversalRequested:
		return d.handleReversal(ctx, evt.AggregateID)
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

// handleInDoubt resolves an in-doubt transfer with a TSQ before deciding —
// never reverse a transfer the rail actually settled (DESIGN-NOTES §1). A
// confirmed settlement completes the settlement leg; a confirmed no-settlement
// (decline) routes to reversal; an answer the rail cannot give after bounded
// retries holds the transfer for MANUAL_REVIEW rather than guessing.
func (d *Driver) handleInDoubt(ctx context.Context, id uuid.UUID) error {
	det, err := d.store.load(ctx, id)
	if err != nil {
		return err
	}
	if det.Status != statusInDoubt {
		return nil // already advanced
	}

	verdict := d.queryStatusWithRetry(ctx, det.Reference)
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
	default: // VerdictUnknown: the rail could not tell us — hold, do not guess.
		_, err := d.store.markManualReview(ctx, id)
		return err
	}
}

// queryStatusWithRetry issues the TSQ up to tsqAttempts times, returning the
// first definitive verdict (success/declined). It returns VerdictUnknown only if
// every attempt was inconclusive.
func (d *Driver) queryStatusWithRetry(ctx context.Context, reference string) RailVerdict {
	for attempt := 0; attempt < d.tsqAttempts; attempt++ {
		if attempt > 0 && d.tsqBackoff > 0 {
			select {
			case <-ctx.Done():
				return VerdictUnknown
			case <-time.After(d.tsqBackoff):
			}
		}
		verdict, err := d.rail.QueryStatus(ctx, reference)
		if err == nil && verdict != VerdictUnknown {
			return verdict
		}
	}
	return VerdictUnknown
}

// handleReversal posts the compensating reversal that restores the source and
// advances reversal_pending -> reversed. It is idempotent: the status guard
// no-ops if the transfer already reversed, and the ledger's per-leg key plus the
// uq_reversal_per_parent index ensure at most one compensating transaction even
// under a re-driven or concurrent delivery (a 23505 is an already-reversed
// no-op; a 40001 is retried inside the ledger).
func (d *Driver) handleReversal(ctx context.Context, id uuid.UUID) error {
	det, err := d.store.load(ctx, id)
	if err != nil {
		return err
	}
	if det.Status != statusReversalPending {
		return nil // already advanced (e.g. already reversed)
	}
	if det.DebitLegTxID == nil {
		return fmt.Errorf("reversal for %s has no debit leg recorded", id)
	}
	if err := d.ledger.PostReversal(ctx, det.transfer(), *det.DebitLegTxID); err != nil {
		return fmt.Errorf("post reversal: %w", err)
	}
	_, err = d.store.markReversed(ctx, id)
	return err
}

// HandleRailCallback applies the rail's asynchronous outcome for a transfer
// (looked up by reference). It is idempotent: the transition methods lock the
// row and no-op if the transfer is already terminal, and the settlement leg is
// keyed, so a duplicate callback — even racing the poller's own settlement —
// can never post a second leg or change a terminal transfer. Returns the
// resulting state for the response.
func (d *Driver) HandleRailCallback(ctx context.Context, reference string, verdict RailVerdict) (State, error) {
	det, err := d.store.loadByReference(ctx, reference)
	if err != nil {
		return "", err
	}
	switch verdict {
	case VerdictSuccess:
		// Settle (idempotent leg). markSettled no-ops if already terminal.
		if !isTerminalStatus(det.Status) {
			if err := d.ledger.PostSettlementLeg(ctx, det.transfer()); err != nil {
				return "", fmt.Errorf("post settlement leg: %w", err)
			}
		}
		if _, err := d.store.markSettled(ctx, det.ID); err != nil {
			return "", err
		}
	case VerdictDeclined:
		if _, err := d.store.markReversalPending(ctx, det.ID, det.eventPayload()); err != nil {
			return "", err
		}
	default:
		// An UNSPECIFIED callback carries no decision; leave the transfer for the
		// in-doubt/TSQ path rather than acting on a non-answer.
	}

	cur, err := d.store.load(ctx, det.ID)
	if err != nil {
		return "", err
	}
	return stateForStatus(cur.Status), nil
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
