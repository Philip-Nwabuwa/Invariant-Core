package logging_test

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/logging"
)

// TestUnaryClientInterceptor_InjectsCorrelationID: the client interceptor copies
// the context's correlation id into the outgoing gRPC metadata so it crosses the
// wire.
func TestUnaryClientInterceptor_InjectsCorrelationID(t *testing.T) {
	ctx := logging.ContextWithCorrelationID(context.Background(), "corr-123")

	var gotMD metadata.MD
	invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		gotMD, _ = metadata.FromOutgoingContext(ctx)
		return nil
	}

	err := logging.UnaryClientInterceptor()(ctx, "/svc/Method", nil, nil, nil, invoker)
	if err != nil {
		t.Fatalf("interceptor: %v", err)
	}
	if got := gotMD.Get(logging.CorrelationMetaKey); len(got) != 1 || got[0] != "corr-123" {
		t.Errorf("outgoing metadata %q = %v, want [corr-123]", logging.CorrelationMetaKey, got)
	}
}

// TestUnaryClientInterceptor_NoIDNoMetadata: with no correlation id on the
// context, the interceptor adds no metadata key.
func TestUnaryClientInterceptor_NoIDNoMetadata(t *testing.T) {
	var gotMD metadata.MD
	invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		gotMD, _ = metadata.FromOutgoingContext(ctx)
		return nil
	}

	if err := logging.UnaryClientInterceptor()(context.Background(), "/svc/Method", nil, nil, nil, invoker); err != nil {
		t.Fatalf("interceptor: %v", err)
	}
	if got := gotMD.Get(logging.CorrelationMetaKey); len(got) != 0 {
		t.Errorf("expected no correlation metadata, got %v", got)
	}
}

// TestUnaryServerInterceptor_ExtractsCorrelationID: the server interceptor reads
// the correlation id from incoming metadata onto the handler's context.
func TestUnaryServerInterceptor_ExtractsCorrelationID(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(logging.CorrelationMetaKey, "corr-456"))

	var seen string
	handler := func(ctx context.Context, _ any) (any, error) {
		seen = logging.CorrelationID(ctx)
		return nil, nil
	}

	_, err := logging.UnaryServerInterceptor()(ctx, nil, &grpc.UnaryServerInfo{}, handler)
	if err != nil {
		t.Fatalf("interceptor: %v", err)
	}
	if seen != "corr-456" {
		t.Errorf("handler context correlation id = %q, want corr-456", seen)
	}
}
