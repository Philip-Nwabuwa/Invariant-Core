-- name: InsertTransfer :one
-- Create the transfer's lifecycle row. type is always 'transfer'; status starts
-- at the coarse 'pending'. idempotency_key is the caller's Idempotency-Key.
-- metadata carries source/destination/amount_minor for the read model (those
-- live in ledger entries, not on this row).
INSERT INTO transactions (reference, type, status, idempotency_key, currency, metadata)
VALUES ($1, 'transfer', $2, $3, $4, $5)
RETURNING *;

-- name: SetTransferStatus :exec
UPDATE transactions
SET status = $2
WHERE id = $1;

-- name: SetTransferSettled :exec
UPDATE transactions
SET status = 'settled', settled_at = now()
WHERE id = $1;

-- name: GetTransferByID :one
SELECT * FROM transactions
WHERE id = $1;
