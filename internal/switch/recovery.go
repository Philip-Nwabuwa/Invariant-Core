package transfer

import (
	"context"
	"log/slog"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/switch/outbox"
)

// Recoverer re-enqueues the driving event for any non-terminal transfer that has
// no live outbox event — a transfer stranded because its event was lost or
// dead-lettered. It is the safety net behind the outbox's own at-least-once
// delivery: run it on startup so a crash can never leave a debit stuck forever.
type Recoverer struct {
	store *PostgresStore
	log   *slog.Logger
}

// NewRecoverer builds a Recoverer over the store.
func NewRecoverer(store *PostgresStore) *Recoverer {
	return &Recoverer{store: store, log: slog.Default()}
}

// Recover re-enqueues the appropriate event for every stuck transfer and returns
// how many it re-enqueued. Re-enqueueing is safe: the driver's handlers are
// idempotent (status-guarded), so a duplicate event is a no-op.
func (r *Recoverer) Recover(ctx context.Context) (int, error) {
	rows, err := r.store.q.ListStuckTransfers(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, row := range rows {
		det := detailFromRow(row)
		evt := eventForStatus(det.Status)
		if evt == "" {
			continue
		}
		if err := outbox.Append(ctx, r.store.q, det.ID, evt, det.eventPayload()); err != nil {
			r.log.Error("recovery re-enqueue failed", "transfer_id", det.ID, "status", det.Status, "error", err)
			continue
		}
		r.log.Info("recovery re-enqueued stuck transfer", "transfer_id", det.ID, "status", det.Status, "event", evt)
		n++
	}
	return n, nil
}

// eventForStatus maps a resumable status to the outbox event that drives it.
func eventForStatus(status string) string {
	switch status {
	case statusPending:
		return outbox.EventDebitRequested
	case statusDebited:
		return outbox.EventDebited
	case statusInDoubt:
		return outbox.EventInDoubt
	case statusReversalPending:
		return outbox.EventReversalRequested
	default:
		return ""
	}
}
