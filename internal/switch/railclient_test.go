package transfer_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	mockrailv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/mockrail/v1"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/mockrail"
	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// dialRail stands up the real mockrail Server over bufconn and returns a client.
func dialRail(t *testing.T) mockrailv1.RailServiceClient {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	mockrailv1.RegisterRailServiceServer(srv, mockrail.NewServer(0))
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
	return mockrailv1.NewRailServiceClient(conn)
}

// TestRailClient_Send proves the switch's Rail adapter talks to the real
// mockrail server and maps a success reply to VerdictSuccess.
func TestRailClient_Send(t *testing.T) {
	rail := transfer.NewRailClient(dialRail(t))

	verdict, err := rail.Send(context.Background(), transfer.Transfer{
		Reference:   "ref-1",
		Source:      "CUST-001",
		Destination: "CUST-002",
		Amount:      money.FromMinor(5000),
		Currency:    "NGN",
	})
	if err != nil {
		t.Fatalf("rail send: %v", err)
	}
	if verdict != transfer.VerdictSuccess {
		t.Fatalf("verdict = %v, want VerdictSuccess", verdict)
	}
}
