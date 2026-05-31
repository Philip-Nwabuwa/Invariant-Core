package transfer_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/testsupport"
	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/switch/outbox"
)

// fakeLedger / fakeRail record calls and can be told to fail, so the orchestrator
// and driver can be exercised without real gRPC clients.
type fakeLedger struct {
	debitErr, settleErr     error
	debitCalls, settleCalls int
}

func (f *fakeLedger) PostDebitLeg(context.Context, transfer.Transfer) (uuid.UUID, error) {
	f.debitCalls++
	return uuid.New(), f.debitErr
}

func (f *fakeLedger) PostSettlementLeg(context.Context, transfer.Transfer) error {
	f.settleCalls++
	return f.settleErr
}

type fakeRail struct {
	verdict transfer.RailVerdict
	sendErr error
	calls   int
}

func (f *fakeRail) Send(context.Context, transfer.Transfer) (transfer.RailVerdict, error) {
	f.calls++
	return f.verdict, f.sendErr
}

// newStack wires the full async stack over a pool for tests.
func newStack(pool *pgxpool.Pool, led transfer.Ledger, rail transfer.Rail) (*transfer.Orchestrator, *transfer.Driver, *transfer.PostgresStore) {
	store := transfer.NewPostgresStore(pool)
	driver := transfer.NewDriver(store, led, rail)
	return transfer.NewOrchestrator(store, driver), driver, store
}

// drainOutbox flushes the outbox synchronously, driving every queued step to a
// terminal state (the poller does this continuously in production).
func drainOutbox(t *testing.T, store *transfer.PostgresStore, driver *transfer.Driver) {
	t.Helper()
	p := outbox.NewPoller(store.Queries(), driver, outbox.Config{Batch: 16})
	if err := p.Drain(context.Background()); err != nil {
		t.Fatalf("drain outbox: %v", err)
	}
}

func TestOrchestrator_HappyPath(t *testing.T) {
	pool := testsupport.NewPool(t) // skips when Docker is unavailable
	led := &fakeLedger{}
	rail := &fakeRail{verdict: transfer.VerdictSuccess}
	o, driver, store := newStack(pool, led, rail)
	ctx := context.Background()

	// POST debits synchronously and returns 202/DEBITED; settlement is async.
	view, err := o.Create(ctx, "key-happy", sampleRequest())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if view.State != transfer.StateDebited {
		t.Errorf("post-create state = %s, want DEBITED", view.State)
	}

	// Drain the outbox: the transfer settles.
	drainOutbox(t, store, driver)

	got, err := o.Get(ctx, view.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != transfer.StateSettled {
		t.Errorf("get state = %s, want SETTLED", got.State)
	}
	if led.debitCalls != 1 || led.settleCalls != 1 || rail.calls != 1 {
		t.Errorf("side-effect calls: debit=%d settle=%d rail=%d, want 1/1/1",
			led.debitCalls, led.settleCalls, rail.calls)
	}

	want := sampleRequest()
	if got.Source != want.Source || got.Destination != want.Destination || got.Amount != want.Amount {
		t.Errorf("get view = %+v, want source/dest/amount from %+v", got, want)
	}
}

func TestOrchestrator_DebitErrorDefersToPoller(t *testing.T) {
	pool := testsupport.NewPool(t)
	boom := errors.New("ledger down") // transient (not a gRPC terminal error)
	led := &fakeLedger{debitErr: boom}
	rail := &fakeRail{verdict: transfer.VerdictSuccess}
	o, _, _ := newStack(pool, led, rail)
	ctx := context.Background()

	// A transient inline debit failure is not fatal: the intent is durable, so
	// Create still returns (202) at DEBIT_PENDING and the rail never ran.
	view, err := o.Create(ctx, "key-fail", sampleRequest())
	if err != nil {
		t.Fatalf("create returned error, want nil (deferred to poller): %v", err)
	}
	if view.State != transfer.StateDebitPending {
		t.Errorf("state = %s, want DEBIT_PENDING", view.State)
	}
	if led.settleCalls != 0 || rail.calls != 0 {
		t.Errorf("after debit failure: settle=%d rail=%d, want 0/0", led.settleCalls, rail.calls)
	}
}

func TestOrchestrator_GetNotFound(t *testing.T) {
	o := transfer.NewOrchestrator(nil, nil)
	if _, err := o.Get(context.Background(), "not-a-uuid"); !errors.Is(err, transfer.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
