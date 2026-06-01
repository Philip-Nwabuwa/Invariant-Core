package transfer

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	switchv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/switch/v1"
)

// GRPCServer is the switch's internal gRPC surface (:50052). Today it serves the
// liveness Ping and the rail-callback intake; the corrective endpoint (Sprint 5)
// lands here too.
type GRPCServer struct {
	switchv1.UnimplementedSwitchServiceServer
	driver *Driver
}

// NewGRPCServer wraps the driver for gRPC serving.
func NewGRPCServer(driver *Driver) *GRPCServer {
	return &GRPCServer{driver: driver}
}

// GRPCServer implements the generated SwitchServiceServer — checked at compile time.
var _ switchv1.SwitchServiceServer = (*GRPCServer)(nil)

// Ping reports liveness.
func (g *GRPCServer) Ping(context.Context, *switchv1.PingRequest) (*switchv1.PingResponse, error) {
	return &switchv1.PingResponse{Ok: true}, nil
}

// RailCallback applies the rail's asynchronous outcome for a transfer. It is
// idempotent (terminal-state guard + keyed settlement leg), so a duplicate
// callback changes nothing.
func (g *GRPCServer) RailCallback(ctx context.Context, req *switchv1.RailCallbackRequest) (*switchv1.RailCallbackResponse, error) {
	if req.GetReference() == "" {
		return nil, status.Error(codes.InvalidArgument, "reference is required")
	}
	state, err := g.driver.HandleRailCallback(ctx, req.GetReference(), verdictFromCallback(req.GetStatus()))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "no transfer for reference %q", req.GetReference())
		}
		return nil, status.Errorf(codes.Internal, "rail callback: %v", err)
	}
	return &switchv1.RailCallbackResponse{State: string(state)}, nil
}

// CorrectiveReversal re-drives a stranded reversal for the referenced transfer
// (the reconcile feedback path, NS-501/502). It is idempotent: the re-driven
// reversal posts at most once (status guard + keyed ledger leg), and a transfer
// that is not awaiting reversal is a no-op (requeued=false).
func (g *GRPCServer) CorrectiveReversal(ctx context.Context, req *switchv1.CorrectiveReversalRequest) (*switchv1.CorrectiveReversalResponse, error) {
	if req.GetReference() == "" {
		return nil, status.Error(codes.InvalidArgument, "reference is required")
	}
	state, requeued, err := g.driver.RequeueReversal(ctx, req.GetReference())
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "no transfer for reference %q", req.GetReference())
		}
		return nil, status.Errorf(codes.Internal, "corrective reversal: %v", err)
	}
	return &switchv1.CorrectiveReversalResponse{State: string(state), Requeued: requeued}, nil
}

// verdictFromCallback maps the callback status enum to a driver verdict.
func verdictFromCallback(s switchv1.CallbackStatus) RailVerdict {
	switch s {
	case switchv1.CallbackStatus_CALLBACK_STATUS_SUCCESS:
		return VerdictSuccess
	case switchv1.CallbackStatus_CALLBACK_STATUS_DECLINED:
		return VerdictDeclined
	default:
		return VerdictUnknown
	}
}
