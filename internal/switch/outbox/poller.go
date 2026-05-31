package outbox

import (
	"context"
	"log/slog"
	"time"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/switch/postgres/switchdb"
)

// Config tunes the poller. Zero fields fall back to defaults (see withDefaults).
type Config struct {
	Interval    time.Duration // tick period
	Batch       int32         // max events claimed per tick
	Lease       time.Duration // how long a claimed event is hidden from other pollers
	MaxAttempts int32         // failed deliveries before an event is dead-lettered
	BaseBackoff time.Duration // first retry delay; doubles each attempt
	MaxBackoff  time.Duration // cap on the retry delay
}

func (c Config) withDefaults() Config {
	if c.Interval <= 0 {
		c.Interval = 500 * time.Millisecond
	}
	if c.Batch <= 0 {
		c.Batch = 64
	}
	if c.Lease <= 0 {
		c.Lease = 30 * time.Second
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 8
	}
	if c.BaseBackoff < 0 {
		c.BaseBackoff = 0
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = 5 * time.Minute
	}
	return c
}

// Poller claims due outbox events and delivers them to a Handler with bounded
// exponential backoff and a dead-letter terminus, so a poison event backs off
// and steps aside instead of head-of-line blocking newer events.
type Poller struct {
	q            *switchdb.Queries
	handler      Handler
	cfg          Config
	log          *slog.Logger
	onDeadLetter func(Event) // optional metrics hook
}

// NewPoller builds a Poller over a pool-scoped Queries and an idempotent Handler.
func NewPoller(q *switchdb.Queries, handler Handler, cfg Config, opts ...Option) *Poller {
	p := &Poller{q: q, handler: handler, cfg: cfg.withDefaults(), log: slog.Default()}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Option customizes a Poller.
type Option func(*Poller)

// WithLogger sets the poller's logger.
func WithLogger(l *slog.Logger) Option { return func(p *Poller) { p.log = l } }

// WithDeadLetterHook registers a callback invoked when an event is dead-lettered.
func WithDeadLetterHook(fn func(Event)) Option { return func(p *Poller) { p.onDeadLetter = fn } }

// Run polls on the configured interval until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) {
	t := time.NewTicker(p.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := p.tick(ctx); err != nil && ctx.Err() == nil {
				p.log.Error("outbox poll failed", "error", err)
			}
		}
	}
}

// Drain processes due events until none remain or ctx is cancelled. Tests and
// the recovery path use it to flush the outbox synchronously. Events the handler
// keeps failing are rescheduled into the future (or dead-lettered), so Drain
// terminates rather than spinning.
func (p *Poller) Drain(ctx context.Context) error {
	for {
		n, err := p.tick(ctx)
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
	}
}

// tick claims one batch and delivers it, returning the number of events handled.
func (p *Poller) tick(ctx context.Context) (int, error) {
	rows, err := p.q.ClaimOutboxBatch(ctx, switchdb.ClaimOutboxBatchParams{
		Secs:  p.cfg.Lease.Seconds(),
		Limit: p.cfg.Batch,
	})
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		evt := Event{
			ID:            row.ID,
			AggregateType: row.AggregateType,
			AggregateID:   row.AggregateID,
			Type:          row.EventType,
			Payload:       row.Payload,
			Attempts:      row.Attempts,
		}
		if herr := p.handler.Handle(ctx, evt); herr != nil {
			p.fail(ctx, evt, herr)
			continue
		}
		if err := p.q.MarkOutboxPublished(ctx, row.ID); err != nil {
			p.log.Error("outbox mark-published failed", "id", row.ID, "error", err)
		}
	}
	return len(rows), nil
}

// fail records a failed delivery: reschedule with backoff, or dead-letter once
// the attempt cap is reached.
func (p *Poller) fail(ctx context.Context, evt Event, herr error) {
	next := evt.Attempts + 1
	msg := herr.Error()
	if next >= p.cfg.MaxAttempts {
		if err := p.q.DeadLetterOutbox(ctx, switchdb.DeadLetterOutboxParams{ID: evt.ID, LastError: &msg}); err != nil {
			p.log.Error("outbox dead-letter failed", "id", evt.ID, "error", err)
			return
		}
		p.log.Error("outbox event dead-lettered", "id", evt.ID, "type", evt.Type, "attempts", next, "error", msg)
		if p.onDeadLetter != nil {
			p.onDeadLetter(evt)
		}
		return
	}
	if err := p.q.RescheduleOutbox(ctx, switchdb.RescheduleOutboxParams{
		ID:        evt.ID,
		Secs:      p.backoff(next).Seconds(),
		LastError: &msg,
	}); err != nil {
		p.log.Error("outbox reschedule failed", "id", evt.ID, "error", err)
	}
}

// backoff returns the delay before the nth retry: BaseBackoff * 2^(n-1), capped.
func (p *Poller) backoff(attempt int32) time.Duration {
	d := p.cfg.BaseBackoff
	for i := int32(1); i < attempt && d < p.cfg.MaxBackoff; i++ {
		d *= 2
	}
	if d > p.cfg.MaxBackoff {
		d = p.cfg.MaxBackoff
	}
	return d
}
