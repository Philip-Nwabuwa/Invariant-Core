package transfer

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// Transfer is the in-flight value the orchestrator hands to the ledger and rail.
type Transfer struct {
	ID          uuid.UUID
	Reference   string
	Source      string
	Destination string
	Amount      money.Amount
	Currency    string
}

// Store is the persistence the orchestrator needs. *TransferStore satisfies it;
// tests can substitute a fake.
type Store interface {
	Create(ctx context.Context, key string, req CreateRequest) (uuid.UUID, error)
	SetStatus(ctx context.Context, id uuid.UUID, status string) error
	SetSettled(ctx context.Context, id uuid.UUID) error
	Get(ctx context.Context, id uuid.UUID) (View, error)
}

// Ledger is the switch's view of the ledger. NS-205 wires the real gRPC client.
type Ledger interface {
	// PostDebitLeg debits the source and credits the settlement account.
	PostDebitLeg(ctx context.Context, t Transfer) error
	// PostSettlementLeg debits settlement and credits the destination.
	PostSettlementLeg(ctx context.Context, t Transfer) error
}

// Rail is the switch's view of the payment rail (mockrail in dev). NS-204 wires
// the real gRPC client.
type Rail interface {
	Send(ctx context.Context, t Transfer) error
}

// Orchestrator implements Service — verified at compile time.
var _ Service = (*Orchestrator)(nil)

// Orchestrator drives a transfer through the happy-path state machine,
// persisting the coarse status at each step. It implements Service. NS-205 adds
// the idempotency store in front of Create.
type Orchestrator struct {
	store  Store
	ledger Ledger
	rail   Rail
}

// NewOrchestrator builds an Orchestrator over its dependencies.
func NewOrchestrator(store Store, ledger Ledger, rail Rail) *Orchestrator {
	return &Orchestrator{store: store, ledger: ledger, rail: rail}
}

// Create runs the happy path synchronously (ARCHITECTURE §4):
//
//	INITIATED → DEBIT_PENDING → DEBITED → AWAITING_SETTLEMENT → SETTLED
//
// It persists the coarse status as it advances and calls the ledger/rail at the
// documented edges. A side-effect error aborts the run, leaving the last
// persisted status in place (recovery is Sprint 3's job).
func (o *Orchestrator) Create(ctx context.Context, key string, req CreateRequest) (View, error) {
	if err := req.Validate(); err != nil {
		return View{}, err
	}

	// INITIATED: create the lifecycle row (status pending).
	id, err := o.store.Create(ctx, key, req)
	if err != nil {
		return View{}, err
	}
	t := Transfer{
		ID:          id,
		Reference:   req.Reference,
		Source:      req.Source,
		Destination: req.Destination,
		Amount:      req.Amount,
		Currency:    req.Currency,
	}

	state := StateInitiated
	// advance moves the machine to "to", running an optional side effect first,
	// then persisting the coarse status. It refuses illegal edges.
	advance := func(to State, side func() error) error {
		if !state.CanTransition(to) {
			return fmt.Errorf("%w: %s -> %s", ErrIllegalTransition, state, to)
		}
		if side != nil {
			if err := side(); err != nil {
				return err
			}
		}
		if to == StateSettled {
			if err := o.store.SetSettled(ctx, id); err != nil {
				return err
			}
		} else if err := o.store.SetStatus(ctx, id, statusForState(to)); err != nil {
			return err
		}
		state = to
		return nil
	}

	// DEBIT_PENDING: idempotency reserved + validated (reservation wired NS-205).
	if err := advance(StateDebitPending, nil); err != nil {
		return View{}, err
	}
	// DEBITED: the ledger debits the source into settlement.
	if err := advance(StateDebited, func() error { return o.ledger.PostDebitLeg(ctx, t) }); err != nil {
		return View{}, err
	}
	// AWAITING_SETTLEMENT: hand off to the rail.
	if err := advance(StateAwaitingSettlement, func() error { return o.rail.Send(ctx, t) }); err != nil {
		return View{}, err
	}
	// SETTLED: the ledger settles from settlement into the destination.
	if err := advance(StateSettled, func() error { return o.ledger.PostSettlementLeg(ctx, t) }); err != nil {
		return View{}, err
	}

	return o.store.Get(ctx, id)
}

// Get returns the current view of a transfer by id.
func (o *Orchestrator) Get(ctx context.Context, id string) (View, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return View{}, ErrNotFound
	}
	return o.store.Get(ctx, uid)
}
