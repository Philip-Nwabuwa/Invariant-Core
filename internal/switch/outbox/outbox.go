// Package outbox implements the transactional outbox (ADR-0004): the switch
// writes a follow-up event in the SAME DB transaction as the state change that
// requires it, and a poller delivers that event at-least-once to an idempotent
// handler. This is what makes "no stranded debit" survive a crash — there is no
// window where a state change is committed but its follow-up work is lost.
package outbox

import (
	"context"

	"github.com/google/uuid"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/switch/postgres/switchdb"
)

// Aggregate + event-type identifiers used across the switch. Each event names a
// step the poller must drive to completion; every step is its own event so a
// crash resumes at the persisted point and never re-runs an earlier side effect.
const (
	// AggregateTransfer tags every switch outbox row (the only aggregate today).
	AggregateTransfer = "transfer"

	// EventDebitRequested: a transfer row exists and intends to debit; the
	// handler posts the debit leg (idempotent by reference) and reconciles.
	EventDebitRequested = "transfer.debit_requested"
	// EventDebited: the source is debited into settlement; the handler sends to
	// the rail and routes to settlement, in-doubt, or reversal.
	EventDebited = "transfer.debited"
	// EventInDoubt: the rail outcome is unknown; the handler issues a TSQ before
	// deciding (never reverses an unconfirmed transfer).
	EventInDoubt = "transfer.in_doubt"
	// EventReversalRequested: a compensating reversal must post to restore source.
	EventReversalRequested = "reversal.requested"
)

// Event is a claimed outbox row handed to a Handler for delivery.
type Event struct {
	ID            int64
	AggregateType string
	AggregateID   uuid.UUID
	Type          string
	Payload       []byte
	Attempts      int32
}

// Handler delivers a single outbox event. Delivery is at-least-once, so a
// Handler MUST be idempotent: handling the same event twice has the same effect
// as once. A returned error reschedules the event with backoff.
type Handler interface {
	Handle(ctx context.Context, evt Event) error
}

// HandlerFunc adapts a plain function to Handler.
type HandlerFunc func(ctx context.Context, evt Event) error

// Handle calls f.
func (f HandlerFunc) Handle(ctx context.Context, evt Event) error { return f(ctx, evt) }

// Append writes a follow-up event. q MUST be tx-scoped (e.g. from a Store
// transaction) so the event and the state change that produced it commit
// together — the no-dual-write guarantee at the heart of crash safety.
func Append(ctx context.Context, q *switchdb.Queries, aggregateID uuid.UUID, eventType string, payload []byte) error {
	_, err := q.InsertOutboxEvent(ctx, switchdb.InsertOutboxEventParams{
		AggregateType: AggregateTransfer,
		AggregateID:   aggregateID,
		EventType:     eventType,
		Payload:       payload,
	})
	return err
}
