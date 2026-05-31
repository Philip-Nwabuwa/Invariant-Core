package logging

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// CorrelationHeader is the HTTP header carrying the correlation id at the REST
// edge. The chi middleware reads it (or generates one) and puts it on the
// request context.
const CorrelationHeader = "X-Correlation-ID"

// CorrelationMetaKey is the gRPC metadata key carrying the correlation id
// between services. gRPC lowercases metadata keys, so this is the canonical form.
const CorrelationMetaKey = "x-correlation-id"

// UnaryClientInterceptor copies the context's correlation id into the outgoing
// gRPC metadata, so a downstream service (ledger, rail) sees the same id. When
// no id is present it leaves the metadata untouched.
func UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if id := CorrelationID(ctx); id != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, CorrelationMetaKey, id)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// UnaryServerInterceptor reads the correlation id from incoming gRPC metadata
// onto the handler's context, so logs on the receiving service carry the same
// id. When the metadata is absent the context is unchanged.
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if vals := md.Get(CorrelationMetaKey); len(vals) > 0 && vals[0] != "" {
				ctx = ContextWithCorrelationID(ctx, vals[0])
			}
		}
		return handler(ctx, req)
	}
}
