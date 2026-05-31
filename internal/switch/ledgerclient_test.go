package transfer_test

import (
	"context"
	"net"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	ledgerv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/ledger/v1"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/ledgerdb"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/testsupport"
	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// dialLedgerOn stands up the real ledger gRPC server over bufconn against the
// given pool and returns a connected client. It seeds the two customer accounts
// the switch's legs move money between (SETTLEMENT is seeded by the migration).
// Taking the pool as an argument lets a test share one database between the
// ledger and the switch's own transactions/idempotency tables.
func dialLedgerOn(t *testing.T, pool *pgxpool.Pool) ledgerv1.LedgerServiceClient {
	t.Helper()
	repo := postgres.NewRepository(pool)
	svc := ledger.NewService(repo)
	ctx := context.Background()
	for _, code := range []string{"CUST-001", "CUST-002"} {
		if _, err := repo.Queries().CreateAccount(ctx, ledgerdb.CreateAccountParams{
			Code: code, Name: code, Type: "asset", Currency: "NGN",
		}); err != nil {
			t.Fatalf("seed account %s: %v", code, err)
		}
	}

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	ledgerv1.RegisterLedgerServiceServer(srv, ledger.NewGRPCServer(svc))
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return ledgerv1.NewLedgerServiceClient(conn)
}

// TestLedgerClient_BothLegsMoveMoneyOnce proves the switch's ledger adapter
// posts the debit leg (source -> SETTLEMENT) and settlement leg
// (SETTLEMENT -> destination) over real gRPC, and that money moves exactly once:
// SETTLEMENT nets back to zero, the source is debited once, the destination is
// credited once.
func TestLedgerClient_BothLegsMoveMoneyOnce(t *testing.T) {
	pool := testsupport.NewPool(t) // skips when Docker is unavailable
	ctx := context.Background()
	client := dialLedgerOn(t, pool)
	led := transfer.NewLedgerClient(client)

	tr := transfer.Transfer{
		Reference:   "REF-LEDGER",
		Source:      "CUST-001",
		Destination: "CUST-002",
		Amount:      money.FromMinor(5000),
		Currency:    "NGN",
	}

	if _, err := led.PostDebitLeg(ctx, tr); err != nil {
		t.Fatalf("PostDebitLeg: %v", err)
	}
	if err := led.PostSettlementLeg(ctx, tr); err != nil {
		t.Fatalf("PostSettlementLeg: %v", err)
	}

	// CUST-001 (asset) debited once: balance = debit - credit = 5000.
	// CUST-002 (asset) credited once: balance = 0 - 5000 = -5000.
	// SETTLEMENT (liability) credited then debited the same amount: nets to 0.
	for _, want := range []struct {
		code    string
		balance int64
	}{
		{"CUST-001", 5000},
		{"CUST-002", -5000},
		{"SETTLEMENT", 0},
	} {
		bal, err := client.GetBalance(ctx, &ledgerv1.GetBalanceRequest{AccountCode: want.code})
		if err != nil {
			t.Fatalf("GetBalance %s: %v", want.code, err)
		}
		if bal.GetBalanceMinor() != want.balance {
			t.Errorf("%s balance = %d, want %d", want.code, bal.GetBalanceMinor(), want.balance)
		}
	}
}
