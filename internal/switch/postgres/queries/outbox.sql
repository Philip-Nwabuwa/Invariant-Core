-- name: InsertOutboxEvent :one
-- Append a follow-up event. Called with a tx-scoped Queries so the event
-- commits in the same transaction as the state change that produced it.
INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload)
VALUES ($1, $2, $3, $4)
RETURNING id;

-- name: ClaimOutboxBatch :many
-- Atomically claim up to $2 due events, hiding them for $1 seconds (a short
-- lease) so a crash mid-delivery re-surfaces the event. All timing is computed
-- against the DB clock (now()) to stay consistent with the claim predicate.
-- FOR UPDATE SKIP LOCKED lets multiple pollers run without contending.
UPDATE outbox
SET next_attempt_at = now() + make_interval(secs => $1)
WHERE id IN (
    SELECT id FROM outbox
    WHERE published_at IS NULL AND dead_letter = false AND next_attempt_at <= now()
    ORDER BY created_at
    LIMIT $2
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: MarkOutboxPublished :exec
UPDATE outbox SET published_at = now() WHERE id = $1;

-- name: RescheduleOutbox :exec
-- Record a failed delivery and back the event off by $2 seconds (DB clock).
UPDATE outbox
SET attempts = attempts + 1, next_attempt_at = now() + make_interval(secs => $2), last_error = $3
WHERE id = $1;

-- name: DeadLetterOutbox :exec
-- Park a poison event so it stops being claimed (it has hit the attempt cap).
UPDATE outbox
SET attempts = attempts + 1, dead_letter = true, last_error = $2
WHERE id = $1;

-- name: OutboxLagSeconds :one
-- Age of the oldest still-deliverable event, for the outbox_lag gauge.
SELECT COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)), 0)::float8 AS lag_seconds
FROM outbox
WHERE published_at IS NULL AND dead_letter = false;

-- name: CountDeadLetterOutbox :one
SELECT count(*) FROM outbox WHERE dead_letter = true;
