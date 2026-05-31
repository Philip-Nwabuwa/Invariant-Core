package logging_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/logging"
)

// correlationCapture wraps the health service to record the correlation id the
// server interceptor placed on the handler context.
type correlationCapture struct {
	healthpb.HealthServer
	seen chan string
}

func (c *correlationCapture) Check(ctx context.Context, _ *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	c.seen <- logging.CorrelationID(ctx)
	return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
}

// TestCorrelationRoundTrip proves the client and server interceptors agree on
// the metadata key: an id set on the caller's context arrives on the handler's
// context across a real gRPC call.
func TestCorrelationRoundTrip(t *testing.T) {
	lis := bufconn.Listen(1024 * 1024)
	capture := &correlationCapture{HealthServer: health.NewServer(), seen: make(chan string, 1)}
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(logging.UnaryServerInterceptor()))
	healthpb.RegisterHealthServer(srv, capture)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(logging.UnaryClientInterceptor()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ctx := logging.ContextWithCorrelationID(context.Background(), "corr-roundtrip")
	if _, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("Check: %v", err)
	}

	if got := <-capture.seen; got != "corr-roundtrip" {
		t.Errorf("server saw correlation id %q, want corr-roundtrip", got)
	}
}
