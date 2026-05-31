-- name: ReserveIdempotencyKey :one
-- Atomically claim a key. ON CONFLICT DO NOTHING means a second concurrent
-- caller gets no row back (pgx.ErrNoRows) rather than an error, so the Go layer
-- can fall through to GetIdempotencyKey and inspect the existing record.
INSERT INTO idempotency_keys (key, request_fingerprint, status)
VALUES ($1, $2, 'in_progress')
ON CONFLICT (key) DO NOTHING
RETURNING *;

-- name: GetIdempotencyKey :one
SELECT * FROM idempotency_keys
WHERE key = $1;

-- name: CompleteIdempotencyKey :exec
-- Record the terminal outcome for a previously reserved key.
UPDATE idempotency_keys
SET status = $2,
    response = $3,
    transaction_id = $4
WHERE key = $1;
