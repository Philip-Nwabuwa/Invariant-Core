package mockrail_test

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	mockrailv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/mockrail/v1"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/mockrail"
)

// dial stands up a mockrail Server with the given latency over an in-memory
// bufconn and returns a connected client.
func dial(t *testing.T, latency time.Duration) mockrailv1.RailServiceClient {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	mockrailv1.RegisterRailServiceServer(srv, mockrail.NewServer(latency))
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

func sampleReq() *mockrailv1.SendToRailRequest {
	return &mockrailv1.SendToRailRequest{
		Reference:   "ref-1",
		Source:      "CUST-001",
		Destination: "CUST-002",
		AmountMinor: 5000,
		Currency:    "NGN",
	}
}

func TestSendToRail_Success(t *testing.T) {
	client := dial(t, 0)
	resp, err := client.SendToRail(context.Background(), sampleReq())
	if err != nil {
		t.Fatalf("SendToRail: %v", err)
	}
	if resp.GetStatus() != mockrailv1.RailStatus_RAIL_STATUS_SUCCESS {
		t.Errorf("status = %s, want SUCCESS", resp.GetStatus())
	}
	if resp.GetRailReference() == "" {
		t.Error("expected a non-empty rail_reference")
	}
}

func TestSendToRail_LatencyRespected(t *testing.T) {
	const latency = 50 * time.Millisecond
	client := dial(t, latency)

	start := time.Now()
	if _, err := client.SendToRail(context.Background(), sampleReq()); err != nil {
		t.Fatalf("SendToRail: %v", err)
	}
	if elapsed := time.Since(start); elapsed < latency {
		t.Errorf("returned after %v, want at least %v", elapsed, latency)
	}
}

func TestSendToRail_ContextCancellation(t *testing.T) {
	client := dial(t, 500*time.Millisecond) // long latency...

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond) // ...short deadline
	defer cancel()

	if _, err := client.SendToRail(ctx, sampleReq()); err == nil {
		t.Error("expected an error when the deadline fires during latency")
	}
}
