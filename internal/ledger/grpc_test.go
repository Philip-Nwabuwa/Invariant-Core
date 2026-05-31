package ledger

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	ledgerv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/ledger/v1"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/ledgerdb"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/testsupport"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// TestCanonicalToProto verifies the canonical.Record -> proto mapping (no DB).
func TestCanonicalToProto(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	settled := now.Add(time.Minute)
	rec := canonical.Record{
		TransactionID: "tx-1", Reference: "REF", Source: "S", Destination: "D",
		AmountMinor: money.FromMinor(12345), Currency: "NGN",
		Type: canonical.TypeTransfer, Status: canonical.StatusSettled,
		InitiatedAt: now, SettledAt: &settled,
		Metadata: map[string]string{"k": "v"},
	}
	p := canonicalToProto(rec)

	if p.GetReference() != "REF" || p.GetSource() != "S" || p.GetDestination() != "D" {
		t.Fatalf("identity fields mismatch: %+v", p)
	}
	if p.GetAmountMinor() != 12345 || p.GetCurrency() != "NGN" {
		t.Fatalf("amount/currency mismatch: %+v", p)
	}
	if p.GetType() != "transfer" || p.GetStatus() != "settled" {
		t.Fatalf("type/status mismatch: %+v", p)
	}
	if !p.GetInitiatedAt().AsTime().Equal(now) {
		t.Fatalf("initiated_at mismatch: %v", p.GetInitiatedAt().AsTime())
	}
	if p.GetSettledAt() == nil || !p.GetSettledAt().AsTime().Equal(settled) {
		t.Fatalf("settled_at mismatch: %v", p.GetSettledAt())
	}
	if p.GetMetadata()["k"] != "v" {
		t.Fatalf("metadata mismatch: %+v", p.GetMetadata())
	}
}

// TestDirectionMapping verifies the proto<->domain direction round trip (no DB).
func TestDirectionMapping(t *testing.T) {
	for _, d := range []Direction{Debit, Credit} {
		if got := directionFromProto(directionToProto(string(d))); got != d {
			t.Fatalf("round trip %q -> %q", d, got)
		}
	}
}

// dialLedger stands up the GRPCServer over an in-memory bufconn and returns a
// connected client.
func dialLedger(t *testing.T, svc *Service) ledgerv1.LedgerServiceClient {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	ledgerv1.RegisterLedgerServiceServer(srv, NewGRPCServer(svc))
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

// TestGRPC_PostThenGetBalance is the gRPC smoke test: post a balanced transfer
// over the wire and read both accounts' balances back (NS-106).
func TestGRPC_PostThenGetBalance(t *testing.T) {
	pool := testsupport.NewPool(t)
	repo := postgres.NewRepository(pool)
	svc := NewService(repo)
	ctx := context.Background()
	if _, err := repo.Queries().CreateAccount(ctx, ledgerdb.CreateAccountParams{
		Code: "CUST-001", Name: "Customer 001", Type: "asset", Currency: "NGN",
	}); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	client := dialLedger(t, svc)

	post, err := client.PostTransaction(ctx, &ledgerv1.PostTransactionRequest{
		Reference: "REF-GRPC",
		Type:      "transfer",
		Entries: []*ledgerv1.EntryInput{
			{AccountCode: "CUST-001", Direction: ledgerv1.Direction_DIRECTION_DEBIT, AmountMinor: 5000, Currency: "NGN"},
			{AccountCode: "SETTLEMENT", Direction: ledgerv1.Direction_DIRECTION_CREDIT, AmountMinor: 5000, Currency: "NGN"},
		},
	})
	if err != nil {
		t.Fatalf("PostTransaction: %v", err)
	}
	if post.GetTransactionId() == "" {
		t.Fatal("empty transaction id")
	}

	cust, err := client.GetBalance(ctx, &ledgerv1.GetBalanceRequest{AccountCode: "CUST-001"})
	if err != nil {
		t.Fatalf("GetBalance CUST: %v", err)
	}
	if cust.GetBalanceMinor() != 5000 {
		t.Fatalf("CUST balance = %d, want 5000", cust.GetBalanceMinor())
	}

	settle, err := client.GetBalance(ctx, &ledgerv1.GetBalanceRequest{AccountCode: "SETTLEMENT"})
	if err != nil {
		t.Fatalf("GetBalance SETTLEMENT: %v", err)
	}
	if settle.GetBalanceMinor() != 5000 {
		t.Fatalf("SETTLEMENT balance = %d, want 5000", settle.GetBalanceMinor())
	}

	entries, err := client.ListEntries(ctx, &ledgerv1.ListEntriesRequest{TransactionId: post.GetTransactionId()})
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries.GetEntries()) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries.GetEntries()))
	}
}
