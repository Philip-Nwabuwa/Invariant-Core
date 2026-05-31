package outbox_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/testsupport"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/switch/outbox"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/switch/postgres/switchdb"
)

// appendEvent writes one outbox row and returns its id.
func appendEvent(t *testing.T, pool *pgxpool.Pool, eventType string) int64 {
	t.Helper()
	q := switchdb.New(pool)
	id, err := q.InsertOutboxEvent(context.Background(), switchdb.InsertOutboxEventParams{
		AggregateType: outbox.AggregateTransfer,
		AggregateID:   uuid.New(),
		EventType:     eventType,
		Payload:       []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("append %s: %v", eventType, err)
	}
	return id
}

// rowState reads the delivery bookkeeping for an outbox row.
func rowState(t *testing.T, pool *pgxpool.Pool, id int64) (published, deadLetter bool, attempts int32) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT published_at IS NOT NULL, dead_letter, attempts FROM outbox WHERE id=$1`, id).
		Scan(&published, &deadLetter, &attempts)
	if err != nil {
		t.Fatalf("row state %d: %v", id, err)
	}
	return published, deadLetter, attempts
}

// countingHandler records how many times each event id was delivered.
type countingHandler struct {
	mu     sync.Mutex
	counts map[int64]int
}

func (h *countingHandler) Handle(_ context.Context, evt outbox.Event) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.counts == nil {
		h.counts = map[int64]int{}
	}
	h.counts[evt.ID]++
	return nil
}

func TestPoller_DeliversEachEventOnce(t *testing.T) {
	t.Parallel()
	pool := testsupport.NewPool(t)
	ctx := context.Background()

	ids := []int64{
		appendEvent(t, pool, outbox.EventDebited),
		appendEvent(t, pool, outbox.EventReversalRequested),
		appendEvent(t, pool, outbox.EventInDoubt),
	}

	h := &countingHandler{}
	p := outbox.NewPoller(switchdb.New(pool), h, outbox.Config{Batch: 10})
	if err := p.Drain(ctx); err != nil {
		t.Fatalf("drain: %v", err)
	}

	for _, id := range ids {
		if got := h.counts[id]; got != 1 {
			t.Errorf("event %d delivered %d times, want 1", id, got)
		}
		if published, _, _ := rowState(t, pool, id); !published {
			t.Errorf("event %d not marked published", id)
		}
	}
}

func TestPoller_BacksOffThenDeadLetters(t *testing.T) {
	t.Parallel()
	pool := testsupport.NewPool(t)
	ctx := context.Background()

	poison := appendEvent(t, pool, "poison")
	good := appendEvent(t, pool, "good")

	var goodDelivered int
	var deadLettered []int64
	handler := outbox.HandlerFunc(func(_ context.Context, evt outbox.Event) error {
		if evt.Type == "poison" {
			return errors.New("permanent failure")
		}
		goodDelivered++
		return nil
	})

	// BaseBackoff 0 makes a failed event immediately due again, so Drain walks
	// it to the attempt cap without real waiting.
	p := outbox.NewPoller(switchdb.New(pool), handler, outbox.Config{
		Batch:       10,
		MaxAttempts: 3,
		BaseBackoff: 0,
	}, outbox.WithDeadLetterHook(func(e outbox.Event) { deadLettered = append(deadLettered, e.ID) }))

	if err := p.Drain(ctx); err != nil {
		t.Fatalf("drain: %v", err)
	}

	// The good event is published exactly once and is NOT blocked by the poison.
	if goodDelivered != 1 {
		t.Errorf("good delivered %d times, want 1", goodDelivered)
	}
	if published, _, _ := rowState(t, pool, good); !published {
		t.Error("good event not published")
	}

	// The poison event is dead-lettered after MaxAttempts failures.
	published, deadLetter, attempts := rowState(t, pool, poison)
	if published {
		t.Error("poison event must not be published")
	}
	if !deadLetter {
		t.Error("poison event must be dead-lettered")
	}
	if attempts != 3 {
		t.Errorf("poison attempts = %d, want 3", attempts)
	}
	if len(deadLettered) != 1 || deadLettered[0] != poison {
		t.Errorf("dead-letter hook fired %v, want [%d]", deadLettered, poison)
	}
}

func TestPoller_OutboxLagGauge(t *testing.T) {
	t.Parallel()
	pool := testsupport.NewPool(t)
	ctx := context.Background()
	q := switchdb.New(pool)

	appendEvent(t, pool, outbox.EventDebited)
	// Backdate it so the lag is unambiguously positive.
	if _, err := pool.Exec(ctx, `UPDATE outbox SET created_at = now() - interval '5 seconds'`); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	lag, err := q.OutboxLagSeconds(ctx)
	if err != nil {
		t.Fatalf("lag: %v", err)
	}
	if lag < 4 {
		t.Errorf("lag = %v, want >= 4", lag)
	}

	p := outbox.NewPoller(q, outbox.HandlerFunc(func(context.Context, outbox.Event) error { return nil }), outbox.Config{Batch: 10})
	if err := p.Drain(ctx); err != nil {
		t.Fatalf("drain: %v", err)
	}

	lag, err = q.OutboxLagSeconds(ctx)
	if err != nil {
		t.Fatalf("lag after drain: %v", err)
	}
	if lag != 0 {
		t.Errorf("lag after drain = %v, want 0", lag)
	}
}
