// Package postgres is the ledger's persistence layer: a pgx connection pool, a
// repository over the sqlc-generated ledgerdb queries, and a SERIALIZABLE
// transaction runner (ADR-0002).
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool opens a pgx connection pool from a Postgres DSN (DB_URL) and verifies
// connectivity with a ping. The caller owns the pool and must Close it.
func NewPool(ctx context.Context, dbURL string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, fmt.Errorf("postgres: new pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return pool, nil
}
