package transfer

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// Transfer is the in-flight value the orchestrator and driver hand to the ledger
// and rail.
type Transfer struct {
	ID          uuid.UUID
	Reference   string
	Source      string
	Destination string
	Amount      money.Amount
	Currency    string
}

// Orchestrator accepts a transfer and starts it on the state machine. The heavy
// lifting (rail, settlement, reversal) is driven asynchronously by the outbox
// poller; Create persists the intent, debits synchronously for a responsive
// 202, and returns. It implements Service.
type Orchestrator struct {
	store  *PostgresStore
	driver *Driver
	log    *slog.Logger
}

// Orchestrator implements Service — verified at compile time.
var _ Service = (*Orchestrator)(nil)

// NewOrchestrator builds an Orchestrator over the store and the driver that
// shares its ledger/rail dependencies.
func NewOrchestrator(store *PostgresStore, driver *Driver) *Orchestrator {
	return &Orchestrator{store: store, driver: driver, log: slog.Default()}
}

// Create validates the request, persists the transfer at DEBIT_PENDING together
// with its transfer.debit_requested outbox event (one transaction — the durable
// intent), then drives the debit inline so the response reflects DEBITED.
//
// The inline debit is best-effort: if it fails transiently, the intent is
// already durable and the poller completes the transfer. The transfer is
// therefore never lost between "row created" and "source debited".
func (o *Orchestrator) Create(ctx context.Context, key string, req CreateRequest) (View, error) {
	if err := req.Validate(); err != nil {
		return View{}, err
	}

	id, err := o.store.CreatePending(ctx, key, req)
	if err != nil {
		return View{}, err
	}

	// Best-effort synchronous debit. A failure here is not fatal: the
	// transfer.debit_requested event is committed, so the poller will post the
	// debit (idempotently) or fail the transfer closed.
	if err := o.driver.handleDebitRequested(ctx, id); err != nil {
		o.log.Warn("inline debit deferred to poller", "transfer_id", id, "error", err)
	}

	return o.store.Get(ctx, id)
}

// Get returns the current view of a transfer by id, or ErrNotFound.
func (o *Orchestrator) Get(ctx context.Context, id string) (View, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return View{}, ErrNotFound
	}
	return o.store.Get(ctx, uid)
}
