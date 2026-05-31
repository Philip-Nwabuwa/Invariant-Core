// Package testsupport spins up an ephemeral PostgreSQL via testcontainers,
// applies the project migrations, and hands back a ready pgx pool. It is the
// shared fixture for the ledger's repository, conservation, and trigger tests
// so they exercise the real SERIALIZABLE path and DB triggers rather than a
// fake. Tests skip cleanly when Docker is unreachable.
package testsupport

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// NewPool starts a throwaway Postgres container, applies migrations/0001_init.up.sql,
// and returns a connected pool. Container and pool teardown are registered via
// t.Cleanup. If Docker is unavailable the test is skipped, not failed.
func NewPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	ctx := context.Background()

	ctr, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.WithDatabase("invariantcore"),
		postgres.WithUsername("invariantcore"),
		postgres.WithPassword("invariantcore"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		if dockerUnavailable(err) {
			t.Skipf("skipping: Docker not available for testcontainers: %v", err)
		}
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = ctr.Terminate(ctx)
	})

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	for _, m := range migrationSQL(t) {
		if _, err := pool.Exec(ctx, m); err != nil {
			t.Fatalf("apply migration: %v", err)
		}
	}

	return pool
}

// migrationSQL reads every migrations/*.up.sql in lexical (version) order so the
// fixture matches the live schema — Sprint 3 adds 0002, future sprints add more.
func migrationSQL(t *testing.T) []string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve caller path")
	}
	// testsupport -> postgres -> ledger -> internal -> repo root
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")
	paths, err := filepath.Glob(filepath.Join(root, "migrations", "*.up.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no up migrations found")
	}
	sort.Strings(paths)
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read migration %s: %v", p, err)
		}
		out = append(out, string(b))
	}
	return out
}

func dockerUnavailable(err error) bool {
	msg := err.Error()
	for _, marker := range []string{
		"Cannot connect to the Docker daemon",
		"docker daemon",
		"rootless Docker not found",
		"failed to find a docker",
		"dial unix",
		"connection refused",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
