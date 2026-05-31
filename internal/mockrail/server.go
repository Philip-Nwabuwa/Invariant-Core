// Package mockrail simulates the NIP rail (ARCHITECTURE §2.3). v1 (NS-204)
// always succeeds after a configurable latency; seedable chaos arrives in
// Sprint 3.
package mockrail

import (
	"context"
	"time"

	"github.com/google/uuid"

	mockrailv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/mockrail/v1"
)

// Server is the rail simulator. It embeds the generated Unimplemented server so
// new RPCs added to the proto don't break the build until implemented.
type Server struct {
	mockrailv1.UnimplementedRailServiceServer
	latency time.Duration
}

// NewServer builds a rail that replies success after latency (0 = immediate).
func NewServer(latency time.Duration) *Server {
	return &Server{latency: latency}
}

// Server implements the generated RailServiceServer — checked at compile time.
var _ mockrailv1.RailServiceServer = (*Server)(nil)

// SendToRail waits out the configured latency (honouring context cancellation),
// then returns a success verdict with a freshly minted rail reference.
func (s *Server) SendToRail(ctx context.Context, req *mockrailv1.SendToRailRequest) (*mockrailv1.SendToRailResponse, error) {
	if s.latency > 0 {
		select {
		case <-time.After(s.latency):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	_ = req // v1 ignores the request fields; chaos in Sprint 3 will key off them.
	return &mockrailv1.SendToRailResponse{
		Status:        mockrailv1.RailStatus_RAIL_STATUS_SUCCESS,
		RailReference: uuid.NewString(),
	}, nil
}

// QueryStatus answers a TSQ: it reports whether the referenced transfer settled.
// The v1 default is SUCCESS after the configured latency; seedable disagreement
// (and a TSQ that itself times out) is layered on by the chaos build (NS-305).
func (s *Server) QueryStatus(ctx context.Context, req *mockrailv1.QueryStatusRequest) (*mockrailv1.QueryStatusResponse, error) {
	if s.latency > 0 {
		select {
		case <-time.After(s.latency):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	_ = req
	return &mockrailv1.QueryStatusResponse{Status: mockrailv1.RailStatus_RAIL_STATUS_SUCCESS}, nil
}
