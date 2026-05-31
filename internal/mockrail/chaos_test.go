package mockrail_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	mockrailv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/mockrail/v1"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/mockrail"
)

// sendOutcome classifies a SendToRail result for the given reference.
func sendOutcome(t *testing.T, srv *mockrail.Server, ref string) string {
	t.Helper()
	resp, err := srv.SendToRail(context.Background(), &mockrailv1.SendToRailRequest{Reference: ref})
	if err != nil {
		if status.Code(err) == codes.DeadlineExceeded {
			return "timeout"
		}
		t.Fatalf("SendToRail(%s): %v", ref, err)
	}
	if resp.GetStatus() == mockrailv1.RailStatus_RAIL_STATUS_DECLINED {
		return "declined"
	}
	return "success"
}

// TestChaos_DeterministicBySeed: two rails with the same seed produce the same
// outcome for every reference, regardless of order — the property NS-307 needs
// for a reproducible chaos run.
func TestChaos_DeterministicBySeed(t *testing.T) {
	cfg := mockrail.Config{Seed: 42, PTimeout: 0.3, PDecline: 0.3}
	a := mockrail.NewServerWithConfig(cfg)
	b := mockrail.NewServerWithConfig(cfg)

	sawDecline, sawTimeout, sawSuccess := false, false, false
	for i := 0; i < 100; i++ {
		ref := fmt.Sprintf("ref-%d", i)
		oa := sendOutcome(t, a, ref)
		ob := sendOutcome(t, b, ref)
		if oa != ob {
			t.Fatalf("ref %s: server A=%s, server B=%s (not deterministic)", ref, oa, ob)
		}
		switch oa {
		case "declined":
			sawDecline = true
		case "timeout":
			sawTimeout = true
		case "success":
			sawSuccess = true
		}
	}
	// Sanity: the probabilities actually produced a mix.
	if !sawDecline || !sawTimeout || !sawSuccess {
		t.Errorf("expected a mix of outcomes, got decline=%v timeout=%v success=%v", sawDecline, sawTimeout, sawSuccess)
	}

	// A different seed yields a different sequence (not all-identical).
	c := mockrail.NewServerWithConfig(mockrail.Config{Seed: 43, PTimeout: 0.3, PDecline: 0.3})
	diff := 0
	for i := 0; i < 100; i++ {
		ref := fmt.Sprintf("ref-%d", i)
		if sendOutcome(t, a, ref) != sendOutcome(t, c, ref) {
			diff++
		}
	}
	if diff == 0 {
		t.Error("a different seed produced an identical sequence")
	}
}

func TestChaos_KnobsTrigger(t *testing.T) {
	ctx := context.Background()

	// PDecline = 1 -> every transfer declined.
	declineAll := mockrail.NewServerWithConfig(mockrail.Config{PDecline: 1})
	if got := sendOutcome(t, declineAll, "any"); got != "declined" {
		t.Errorf("PDecline=1 outcome = %s, want declined", got)
	}

	// PTimeout = 1 -> SendToRail always times out.
	timeoutAll := mockrail.NewServerWithConfig(mockrail.Config{PTimeout: 1})
	if got := sendOutcome(t, timeoutAll, "any"); got != "timeout" {
		t.Errorf("PTimeout=1 outcome = %s, want timeout", got)
	}

	// PTSQTimeout = 1 -> QueryStatus always times out.
	tsqTimeout := mockrail.NewServerWithConfig(mockrail.Config{PTSQTimeout: 1})
	if _, err := tsqTimeout.QueryStatus(ctx, &mockrailv1.QueryStatusRequest{Reference: "any"}); status.Code(err) != codes.DeadlineExceeded {
		t.Errorf("PTSQTimeout=1 err = %v, want DeadlineExceeded", err)
	}

	// TSQ reports the true outcome and can disagree with a timed-out send: a
	// transfer that times out on send but is not a decline settles per the TSQ.
	settledTruth := mockrail.NewServerWithConfig(mockrail.Config{PTimeout: 1, PDecline: 0})
	if got := sendOutcome(t, settledTruth, "any"); got != "timeout" {
		t.Fatalf("send outcome = %s, want timeout", got)
	}
	resp, err := settledTruth.QueryStatus(ctx, &mockrailv1.QueryStatusRequest{Reference: "any"})
	if err != nil {
		t.Fatalf("QueryStatus: %v", err)
	}
	if resp.GetStatus() != mockrailv1.RailStatus_RAIL_STATUS_SUCCESS {
		t.Errorf("TSQ for a timed-out-but-settled transfer = %s, want SUCCESS", resp.GetStatus())
	}
}

// recordingSender counts the duplicate callbacks mockrail fires.
type recordingSender struct {
	mu    sync.Mutex
	count int
	ch    chan struct{}
}

func (r *recordingSender) SendCallback(string, bool) {
	r.mu.Lock()
	r.count++
	r.mu.Unlock()
	r.ch <- struct{}{}
}

func TestChaos_DuplicateCallbacksFire(t *testing.T) {
	sender := &recordingSender{ch: make(chan struct{}, 4)}
	srv := mockrail.NewServerWithConfig(mockrail.Config{PDuplicate: 1, Callback: sender})

	if got := sendOutcome(t, srv, "ref-dup"); got != "success" {
		t.Fatalf("outcome = %s, want success", got)
	}
	// Two duplicate callbacks fire asynchronously.
	for i := 0; i < 2; i++ {
		select {
		case <-sender.ch:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for duplicate callback %d", i+1)
		}
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if sender.count != 2 {
		t.Errorf("duplicate callbacks = %d, want 2", sender.count)
	}
}
