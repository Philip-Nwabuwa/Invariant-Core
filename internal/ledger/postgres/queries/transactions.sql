-- name: InsertTransaction :one
INSERT INTO transactions (
    reference, type, status, idempotency_key, parent_transaction_id, currency, metadata
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetTransaction :one
SELECT * FROM transactions
WHERE id = $1;

-- name: ListTransactionsByWindow :many
SELECT * FROM transactions
WHERE initiated_at >= $1
  AND initiated_at < $2
ORDER BY initiated_at, id;
