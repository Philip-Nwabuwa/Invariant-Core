-- name: GetCachedBalance :one
SELECT * FROM account_balances
WHERE account_id = $1;

-- name: UpsertCachedBalance :exec
INSERT INTO account_balances (account_id, balance_minor, currency, updated_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (account_id) DO UPDATE
SET balance_minor = EXCLUDED.balance_minor,
    currency      = EXCLUDED.currency,
    updated_at    = now();
