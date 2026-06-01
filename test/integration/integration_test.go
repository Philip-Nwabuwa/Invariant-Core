//go:build integration

// Package integration_test is the Sprint-5 testcontainers integration suite
// (NS-503, NFR-9). Unlike the unit tests sprinkled through the packages, these
// run only under `-tags=integration` (via `make test-integration` and the CI
// integration step) and exercise the real stack end-to-end over an ephemeral
// Postgres: serializable ledger posting, idempotent transfer replay, and
// reversal recovery after a simulated switchd restart.
//
// The harness is in-process — a real ledger gRPC server and the real mockrail
// simulator over bufconn, driving the real Postgres-backed orchestrator, driver,
// and outbox — mirroring test/chaos. It skips cleanly when Docker is absent.
package integration_test

import (
	"context"
	"net"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	ledgerv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/ledger/v1"
	mockrailv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/mockrail/v1"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger"
	ledgerpg "github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/ledgerdb"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/mockrail"
	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
)

const (
	srcAccount   = "CUST-001"
	dstAccount   = "CUST-002"
	suspenseAcct = "SETTLEMENT"
)

// dialLedger stands up the real ledger gRPC server over bufconn against the pool
// and seeds the two customer accounts (SETTLEMENT is seeded by the migration).
func dialLedger(t *testing.T, pool *pgxpool.Pool) ledgerv1.LedgerServiceClient {
	t.Helper()
	repo := ledgerpg.NewRepository(pool)
	svc := ledger.NewService(repo)
	ctx := context.Background()
	for _, code := range []string{srcAccount, dstAccount} {
		if _, err := repo.Queries().CreateAccount(ctx, ledgerdb.CreateAccountParams{
			Code: code, Name: code, Type: "asset", Currency: "NGN",
		}); err != nil {
			t.Fatalf("seed account %s: %v", code, err)
		}
	}
	conn := serveBufconn(t, func(s *grpc.Server) { ledgerv1.RegisterLedgerServiceServer(s, ledger.NewGRPCServer(svc)) })
	return ledgerv1.NewLedgerServiceClient(conn)
}

// dialRail serves the given rail simulator over bufconn and returns a client.
func dialRail(t *testing.T, srv *mockrail.Server) mockrailv1.RailServiceClient {
	t.Helper()
	conn := serveBufconn(t, func(s *grpc.Server) { mockrailv1.RegisterRailServiceServer(s, srv) })
	return mockrailv1.NewRailServiceClient(conn)
}

func serveBufconn(t *testing.T, register func(*grpc.Server)) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	s := grpc.NewServer()
	register(s)
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func assertBalance(ctx context.Context, t *testing.T, c ledgerv1.LedgerServiceClient, code string, want int64) {
	t.Helper()
	bal, err := c.GetBalance(ctx, &ledgerv1.GetBalanceRequest{AccountCode: code})
	if err != nil {
		t.Fatalf("GetBalance %s: %v", code, err)
	}
	if bal.GetBalanceMinor() != want {
		t.Errorf("%s balance = %d, want %d", code, bal.GetBalanceMinor(), want)
	}
}

func countNonTerminal(ctx context.Context, t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var c int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM transactions
		 WHERE type='transfer' AND status IN ('pending','debited','in_doubt','reversal_pending')`).
		Scan(&c); err != nil {
		t.Fatalf("count non-terminal: %v", err)
	}
	return c
}

// transferRowCount counts the lifecycle rows for a reference (a transfer row
// carries the 'source' metadata key; ledger legs do not).
func transferRowCount(ctx context.Context, t *testing.T, pool *pgxpool.Pool, ref string) int {
	t.Helper()
	var c int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM transactions WHERE reference=$1 AND metadata ? 'source'`, ref).
		Scan(&c); err != nil {
		t.Fatalf("count transfer rows %s: %v", ref, err)
	}
	return c
}

// newIdempotentSwitch wires the production transfer service: orchestrator +
// driver behind the IdempotentService decorator, exactly as cmd/switchd does.
func newIdempotentSwitch(pool *pgxpool.Pool, led transfer.Ledger, rail transfer.Rail) (transfer.Service, *transfer.Driver, *transfer.PostgresStore) {
	store := transfer.NewPostgresStore(pool)
	driver := transfer.NewDriver(store, led, rail)
	orchestrator := transfer.NewOrchestrator(store, driver)
	svc := transfer.NewIdempotentService(orchestrator, transfer.NewIdempotencyStore(pool))
	return svc, driver, store
}
