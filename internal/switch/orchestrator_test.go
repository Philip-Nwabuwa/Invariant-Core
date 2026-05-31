package transfer_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/testsupport"
	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
)

// fakeLedger / fakeRail record calls and can be told to fail, so the orchestrator
// can be exercised without real gRPC clients (those arrive in NS-204/205).
type fakeLedger struct {
	debitErr, settleErr     error
	debitCalls, settleCalls int
}

func (f *fakeLedger) PostDebitLeg(context.Context, transfer.Transfer) error {
	f.debitCalls++
	return f.debitErr
}

func (f *fakeLedger) PostSettlementLeg(context.Context, transfer.Transfer) error {
	f.settleCalls++
	return f.settleErr
}

type fakeRail struct {
	sendErr error
	calls   int
}

func (f *fakeRail) Send(context.Context, transfer.Transfer) error {
	f.calls++
	return f.sendErr
}

func TestOrchestrator_HappyPath(t *testing.T) {
	pool := testsupport.NewPool(t) // skips when Docker is unavailable
	store := transfer.NewPostgresStore(pool)
	led := &fakeLedger{}
	rail := &fakeRail{}
	o := transfer.NewOrchestrator(store, led, rail)
	ctx := context.Background()

	view, err := o.Create(ctx, "key-happy", sampleRequest())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if view.State != transfer.StateSettled {
		t.Errorf("state = %s, want SETTLED", view.State)
	}
	if led.debitCalls != 1 || led.settleCalls != 1 || rail.calls != 1 {
		t.Errorf("side-effect calls: debit=%d settle=%d rail=%d, want 1/1/1",
			led.debitCalls, led.settleCalls, rail.calls)
	}

	// GET re-reads from the DB and reconstructs source/destination/amount.
	got, err := o.Get(ctx, view.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != transfer.StateSettled {
		t.Errorf("get state = %s, want SETTLED", got.State)
	}
	want := sampleRequest()
	if got.Source != want.Source || got.Destination != want.Destination || got.Amount != want.Amount {
		t.Errorf("get view = %+v, want source/dest/amount from %+v", got, want)
	}
}

func TestOrchestrator_DebitErrorAborts(t *testing.T) {
	pool := testsupport.NewPool(t)
	store := transfer.NewPostgresStore(pool)
	boom := errors.New("ledger down")
	led := &fakeLedger{debitErr: boom}
	rail := &fakeRail{}
	o := transfer.NewOrchestrator(store, led, rail)
	ctx := context.Background()

	_, err := o.Create(ctx, "key-fail", sampleRequest())
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	// The settlement leg and rail must not have run.
	if led.settleCalls != 0 || rail.calls != 0 {
		t.Errorf("after debit failure: settle=%d rail=%d, want 0/0", led.settleCalls, rail.calls)
	}
}

func TestOrchestrator_GetNotFound(t *testing.T) {
	o := transfer.NewOrchestrator(nil, nil, nil)
	if _, err := o.Get(context.Background(), "not-a-uuid"); !errors.Is(err, transfer.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
