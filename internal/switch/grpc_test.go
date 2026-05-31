package transfer_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	switchv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/switch/v1"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/ledger/postgres/testsupport"
	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// dialSwitch stands up the switch gRPC server over bufconn and returns a client.
func dialSwitch(t *testing.T, driver *transfer.Driver) switchv1.SwitchServiceClient {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	switchv1.RegisterSwitchServiceServer(srv, transfer.NewGRPCServer(driver))
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
	return switchv1.NewSwitchServiceClient(conn)
}

// TestRailCallback_DuplicateIsNoOp: a SUCCESS callback settles a debited
// transfer; a second (duplicate) callback changes nothing — one settlement leg,
// balances unchanged, state still SETTLED.
func TestRailCallback_DuplicateIsNoOp(t *testing.T) {
	pool := testsupport.NewPool(t) // skips when Docker is unavailable
	ctx := context.Background()
	rawLedger := dialLedgerOn(t, pool)
	store := transfer.NewPostgresStore(pool)
	// A rail whose Send is unknown: the poller would leave the transfer debited,
	// so the callback is what settles it here.
	driver := transfer.NewDriver(store, transfer.NewLedgerClient(rawLedger), &fakeRail{verdict: transfer.VerdictUnknown})
	o := transfer.NewOrchestrator(store, driver)

	const ref = "REF-CALLBACK"
	if _, err := o.Create(ctx, "key-cb", transfer.CreateRequest{
		Source: "CUST-001", Destination: "CUST-002", Amount: money.FromMinor(5000), Currency: "NGN", Reference: ref,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	client := dialSwitch(t, driver)

	first, err := client.RailCallback(ctx, &switchv1.RailCallbackRequest{Reference: ref, Status: switchv1.CallbackStatus_CALLBACK_STATUS_SUCCESS})
	if err != nil {
		t.Fatalf("first callback: %v", err)
	}
	if first.GetState() != string(transfer.StateSettled) {
		t.Fatalf("state after callback = %s, want SETTLED", first.GetState())
	}

	// Duplicate callback: no-op.
	dup, err := client.RailCallback(ctx, &switchv1.RailCallbackRequest{Reference: ref, Status: switchv1.CallbackStatus_CALLBACK_STATUS_SUCCESS})
	if err != nil {
		t.Fatalf("duplicate callback: %v", err)
	}
	if dup.GetState() != string(transfer.StateSettled) {
		t.Errorf("state after duplicate = %s, want SETTLED", dup.GetState())
	}

	// Exactly one settlement leg, SETTLEMENT nets to zero, destination credited once.
	var settleLegs int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM transactions WHERE reference=$1 AND idempotency_key LIKE '%:settle'`, ref).
		Scan(&settleLegs); err != nil {
		t.Fatalf("count settle legs: %v", err)
	}
	if settleLegs != 1 {
		t.Errorf("settlement legs = %d, want 1 (duplicate callback must not double-settle)", settleLegs)
	}
	assertBalances(t, rawLedger, map[string]int64{"CUST-001": 5000, "CUST-002": -5000, "SETTLEMENT": 0})
}

// TestRailCallback_UnknownReference returns NotFound.
func TestRailCallback_UnknownReference(t *testing.T) {
	pool := testsupport.NewPool(t)
	rawLedger := dialLedgerOn(t, pool)
	store := transfer.NewPostgresStore(pool)
	driver := transfer.NewDriver(store, transfer.NewLedgerClient(rawLedger), &fakeRail{})
	client := dialSwitch(t, driver)

	_, err := client.RailCallback(context.Background(), &switchv1.RailCallbackRequest{
		Reference: "does-not-exist", Status: switchv1.CallbackStatus_CALLBACK_STATUS_SUCCESS,
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("err = %v, want NotFound", err)
	}
}
