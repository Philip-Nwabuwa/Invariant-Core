package transfer

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

// StubService is a placeholder Service used only until the real orchestrator
// lands (NS-203/205). It validates the request, assigns an id, immediately marks
// the transfer SETTLED, and keeps it in memory so a POST followed by a GET works
// for a manual smoke test. It does NOT touch the rail, the ledger, or Postgres.
//
// TODO(NS-205): delete StubService and wire the real Orchestrator.
type StubService struct {
	mu    sync.Mutex
	store map[string]View
}

// NewStubService returns an empty in-memory stub.
func NewStubService() *StubService {
	return &StubService{store: make(map[string]View)}
}

// Create validates and records a transfer as immediately SETTLED.
func (s *StubService) Create(_ context.Context, _ string, req CreateRequest) (View, error) {
	if err := req.Validate(); err != nil {
		return View{}, err
	}
	view := View{
		ID:          uuid.NewString(),
		Reference:   req.Reference,
		Source:      req.Source,
		Destination: req.Destination,
		Amount:      req.Amount,
		Currency:    req.Currency,
		State:       StateSettled,
	}
	s.mu.Lock()
	s.store[view.ID] = view
	s.mu.Unlock()
	return view, nil
}

// Get returns a previously created transfer, or ErrNotFound.
func (s *StubService) Get(_ context.Context, id string) (View, error) {
	s.mu.Lock()
	view, ok := s.store[id]
	s.mu.Unlock()
	if !ok {
		return View{}, ErrNotFound
	}
	return view, nil
}
