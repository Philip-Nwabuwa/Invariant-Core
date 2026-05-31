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

-- name: GetTransferForUpdate :one
-- Lock the transfer row so a status advance (settle / reverse / callback) is
-- serialized against any other driver or callback touching the same transfer.
SELECT * FROM transactions
WHERE id = $1
FOR UPDATE;

-- name: SetTransferStatusAndDebitLeg :exec
-- Advance to 'debited' and record the debit leg's ledger transaction id (the
-- reversal parent) in metadata, in one statement.
UPDATE transactions
SET status = sqlc.arg(status),
    metadata = metadata || jsonb_build_object('debit_leg_tx_id', sqlc.arg(debit_leg_tx_id)::text)
WHERE id = sqlc.arg(id);

-- name: GetTransferByReference :one
-- Find the switch's lifecycle transfer row for a reference. The `metadata ?
-- 'source'` predicate selects the lifecycle row (which carries source/dest/
-- amount) over the ledger legs that share the reference but have empty metadata.
SELECT * FROM transactions
WHERE reference = $1 AND metadata ? 'source'
ORDER BY initiated_at DESC
LIMIT 1;

-- name: ListResumableTransfers :many
-- Non-terminal transfers, for the recovery sweep (NS-306). Ordered oldest-first.
SELECT * FROM transactions
WHERE type = 'transfer'
  AND status IN ('pending','debited','in_doubt','reversal_pending')
ORDER BY initiated_at;
