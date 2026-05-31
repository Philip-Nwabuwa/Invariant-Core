-- name: CreateAccount :one
INSERT INTO accounts (code, name, type, currency)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetAccountByCode :one
SELECT * FROM accounts
WHERE code = $1;

-- name: GetAccountByID :one
SELECT * FROM accounts
WHERE id = $1;
