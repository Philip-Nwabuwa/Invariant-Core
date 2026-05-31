package transfer_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
)

// fakeService is a Service stub the decorator delegates to. It records calls and
// can be told to fail.
type fakeService struct {
	view       transfer.View
	createErr  error
	createCall int
}

func (f *fakeService) Create(context.Context, string, transfer.CreateRequest) (transfer.View, error) {
	f.createCall++
	return f.view, f.createErr
}

func (f *fakeService) Get(context.Context, string) (transfer.View, error) {
	return f.view, nil
}

// fakeIdem is an Idempotency stub returning a canned Reserve outcome and
// recording the Complete call.
type fakeIdem struct {
	result       transfer.ReserveResult
	reserveErr   error
	completeCall int
	gotStatus    string
	gotTxID      *uuid.UUID
	gotResponse  []byte
}

func (f *fakeIdem) Reserve(context.Context, string, string) (transfer.ReserveResult, error) {
	return f.result, f.reserveErr
}

func (f *fakeIdem) Complete(_ context.Context, _ string, status string, txID *uuid.UUID, response []byte) error {
	f.completeCall++
	f.gotStatus = status
	f.gotTxID = txID
	f.gotResponse = response
	return nil
}

// TestIdempotentService_ReservedDelegatesAndCompletes: a brand-new key runs the
// inner service once, then records the result as succeeded with the transaction
// id and the marshalled response.
func TestIdempotentService_ReservedDelegatesAndCompletes(t *testing.T) {
	id := uuid.New()
	inner := &fakeService{view: transfer.View{ID: id.String(), State: transfer.StateSettled}}
	idem := &fakeIdem{result: transfer.ReserveResult{Outcome: transfer.OutcomeReserved}}
	svc := transfer.NewIdempotentService(inner, idem)

	view, err := svc.Create(context.Background(), "key-1", sampleRequest())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if view.State != transfer.StateSettled {
		t.Errorf("state = %s, want SETTLED", view.State)
	}
	if inner.createCall != 1 {
		t.Errorf("inner Create calls = %d, want 1", inner.createCall)
	}
	if idem.completeCall != 1 {
		t.Errorf("Complete calls = %d, want 1", idem.completeCall)
	}
	if idem.gotStatus != transfer.IdemSucceeded {
		t.Errorf("Complete status = %q, want %q", idem.gotStatus, transfer.IdemSucceeded)
	}
	if idem.gotTxID == nil || *idem.gotTxID != id {
		t.Errorf("Complete txID = %v, want %v", idem.gotTxID, id)
	}
	if len(idem.gotResponse) == 0 {
		t.Error("Complete response is empty, want marshalled view")
	}
}

// TestIdempotentService_ReplayReturnsStoredResponse: a completed key returns the
// stored response verbatim and never touches the inner service.
func TestIdempotentService_ReplayReturnsStoredResponse(t *testing.T) {
	stored := transfer.View{ID: uuid.NewString(), Reference: "ref-1", State: transfer.StateSettled}
	body, _ := json.Marshal(stored)
	inner := &fakeService{}
	idem := &fakeIdem{result: transfer.ReserveResult{Outcome: transfer.OutcomeReplay, Response: body}}
	svc := transfer.NewIdempotentService(inner, idem)

	view, err := svc.Create(context.Background(), "key-1", sampleRequest())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if view != stored {
		t.Errorf("view = %+v, want stored %+v", view, stored)
	}
	if inner.createCall != 0 {
		t.Errorf("inner Create calls = %d, want 0 (replay must not re-process)", inner.createCall)
	}
}

// TestIdempotentService_Conflict: a key reused with a different body is a
// conflict; the inner service is never called.
func TestIdempotentService_Conflict(t *testing.T) {
	inner := &fakeService{}
	idem := &fakeIdem{result: transfer.ReserveResult{Outcome: transfer.OutcomeConflict}}
	svc := transfer.NewIdempotentService(inner, idem)

	_, err := svc.Create(context.Background(), "key-1", sampleRequest())
	if !errors.Is(err, transfer.ErrIdempotencyConflict) {
		t.Fatalf("err = %v, want ErrIdempotencyConflict", err)
	}
	if inner.createCall != 0 {
		t.Errorf("inner Create calls = %d, want 0", inner.createCall)
	}
}

// TestIdempotentService_InProgress: a concurrent in-flight key returns
// ErrInProgress without re-processing.
func TestIdempotentService_InProgress(t *testing.T) {
	inner := &fakeService{}
	idem := &fakeIdem{result: transfer.ReserveResult{Outcome: transfer.OutcomeInProgress}}
	svc := transfer.NewIdempotentService(inner, idem)

	_, err := svc.Create(context.Background(), "key-1", sampleRequest())
	if !errors.Is(err, transfer.ErrInProgress) {
		t.Fatalf("err = %v, want ErrInProgress", err)
	}
	if inner.createCall != 0 {
		t.Errorf("inner Create calls = %d, want 0", inner.createCall)
	}
}

// TestIdempotentService_InnerErrorMarksFailed: when the inner service fails, the
// key is recorded failed (so a retry can proceed) and the error propagates.
func TestIdempotentService_InnerErrorMarksFailed(t *testing.T) {
	boom := errors.New("orchestrator down")
	inner := &fakeService{createErr: boom}
	idem := &fakeIdem{result: transfer.ReserveResult{Outcome: transfer.OutcomeReserved}}
	svc := transfer.NewIdempotentService(inner, idem)

	_, err := svc.Create(context.Background(), "key-1", sampleRequest())
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if idem.completeCall != 1 || idem.gotStatus != transfer.IdemFailed {
		t.Errorf("Complete status = %q (calls %d), want %q", idem.gotStatus, idem.completeCall, transfer.IdemFailed)
	}
}
