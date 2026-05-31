-- name: InsertEntry :one
INSERT INTO entries (transaction_id, account_id, direction, amount_minor, currency)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListEntriesByTransaction :many
SELECT * FROM entries
WHERE transaction_id = $1
ORDER BY created_at, id;

-- name: ListEntriesByAccount :many
SELECT * FROM entries
WHERE account_id = $1
ORDER BY created_at, id;

-- name: SumEntriesByAccount :one
SELECT
    COALESCE(SUM(amount_minor) FILTER (WHERE direction = 'debit'), 0)::bigint  AS debit_minor,
    COALESCE(SUM(amount_minor) FILTER (WHERE direction = 'credit'), 0)::bigint AS credit_minor
FROM entries
WHERE account_id = $1;
