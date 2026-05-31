// Package mockrail simulates the NIP rail (ARCHITECTURE §2.3): a deterministic-
// by-seed source of the chaos the switch must survive — added latency, hard
// timeouts (no answer), explicit declines, and duplicate success callbacks. Each
// transfer's outcome is derived from hash(seed, reference), so a run is
// reproducible regardless of concurrency or arrival order.
package mockrail

import (
	"context"
	"hash/fnv"
	"math"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	mockrailv1 "github.com/Philip-Nwabuwa/Invariant-Core/api/gen/mockrail/v1"
)

// CallbackSender delivers an asynchronous rail callback to the switch. It is the
// seam mockrail uses to inject duplicate success callbacks without importing the
// switch package; cmd/mockrail wires a switch gRPC client behind it.
type CallbackSender interface {
	SendCallback(reference string, declined bool)
}

// Config tunes the rail simulator. The zero value is a no-chaos rail that always
// succeeds immediately (the NS-204 behaviour).
type Config struct {
	Latency     time.Duration
	Seed        int64
	PTimeout    float64 // SendToRail returns no answer (the in-doubt case)
	PDecline    float64 // the transfer's true outcome is a decline
	PDuplicate  float64 // a successful transfer also gets duplicate callbacks
	PTSQTimeout float64 // QueryStatus itself times out
	Callback    CallbackSender
}

// Server is the rail simulator.
type Server struct {
	mockrailv1.UnimplementedRailServiceServer
	cfg Config
}

// NewServer builds a no-chaos rail that replies success after latency.
func NewServer(latency time.Duration) *Server {
	return &Server{cfg: Config{Latency: latency}}
}

// NewServerWithConfig builds a rail with the given chaos configuration.
func NewServerWithConfig(cfg Config) *Server {
	return &Server{cfg: cfg}
}

// Server implements the generated RailServiceServer — checked at compile time.
var _ mockrailv1.RailServiceServer = (*Server)(nil)

// SendToRail returns the transfer's outcome, deterministic by reference: a
// timeout hides the answer (in-doubt), a decline refuses it, otherwise success.
// A successful, duplicate-flagged transfer also fires extra callbacks so the
// switch's idempotent intake is exercised.
func (s *Server) SendToRail(ctx context.Context, req *mockrailv1.SendToRailRequest) (*mockrailv1.SendToRailResponse, error) {
	if err := s.delay(ctx); err != nil {
		return nil, err
	}
	ref := req.GetReference()
	if s.fires(ref, "send_timeout", s.cfg.PTimeout) {
		return nil, status.Error(codes.DeadlineExceeded, "rail timed out (no answer)")
	}
	if s.trueDecline(ref) {
		return &mockrailv1.SendToRailResponse{Status: mockrailv1.RailStatus_RAIL_STATUS_DECLINED}, nil
	}
	if s.cfg.Callback != nil && s.fires(ref, "duplicate", s.cfg.PDuplicate) {
		// Asynchronous duplicate success callbacks (the switch settles via the
		// poller; these must be idempotent no-ops). Fired twice on purpose.
		go s.cfg.Callback.SendCallback(ref, false)
		go s.cfg.Callback.SendCallback(ref, false)
	}
	return &mockrailv1.SendToRailResponse{
		Status:        mockrailv1.RailStatus_RAIL_STATUS_SUCCESS,
		RailReference: uuid.NewString(),
	}, nil
}

// QueryStatus answers a TSQ with the transfer's TRUE outcome (which may disagree
// with a timed-out SendToRail — the "settled-but-we-timed-out" case), unless the
// TSQ itself times out.
func (s *Server) QueryStatus(ctx context.Context, req *mockrailv1.QueryStatusRequest) (*mockrailv1.QueryStatusResponse, error) {
	if err := s.delay(ctx); err != nil {
		return nil, err
	}
	ref := req.GetReference()
	if s.fires(ref, "tsq_timeout", s.cfg.PTSQTimeout) {
		return nil, status.Error(codes.DeadlineExceeded, "tsq timed out")
	}
	if s.trueDecline(ref) {
		return &mockrailv1.QueryStatusResponse{Status: mockrailv1.RailStatus_RAIL_STATUS_DECLINED}, nil
	}
	return &mockrailv1.QueryStatusResponse{Status: mockrailv1.RailStatus_RAIL_STATUS_SUCCESS}, nil
}

// delay waits out the configured latency, honouring context cancellation.
func (s *Server) delay(ctx context.Context) error {
	if s.cfg.Latency <= 0 {
		return nil
	}
	select {
	case <-time.After(s.cfg.Latency):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// trueDecline reports whether the transfer's underlying outcome is a decline.
func (s *Server) trueDecline(ref string) bool {
	return s.fires(ref, "decline", s.cfg.PDecline)
}

// fires reports whether the probability p triggers for (reference, dimension),
// deterministically: u01(seed, ref, dim) < p. Independent dimensions give
// independent decisions for the same reference.
func (s *Server) fires(ref, dimension string, p float64) bool {
	if p <= 0 {
		return false
	}
	if p >= 1 {
		return true
	}
	return u01(s.cfg.Seed, ref, dimension) < p
}

// u01 maps (seed, reference, dimension) to a stable value in [0, 1).
func u01(seed int64, ref, dimension string) float64 {
	h := fnv.New64a()
	var seedBytes [8]byte
	for i := 0; i < 8; i++ {
		seedBytes[i] = byte(seed >> (8 * i))
	}
	_, _ = h.Write(seedBytes[:])
	_, _ = h.Write([]byte(ref))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(dimension))
	return float64(h.Sum64()) / (float64(math.MaxUint64) + 1)
}
