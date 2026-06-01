package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
)

// stubService is an in-memory transfer.Service used only by these transport
// tests: it validates the request, assigns an id, marks the transfer DEBITED
// (the async 202 state), and keeps it in memory so a POST followed by a GET
// round-trips. It never
// touches the rail, the ledger, or Postgres — the HTTP edge is what's under test.
type stubService struct {
	mu    sync.Mutex
	store map[string]transfer.View
}

func newStubService() *stubService {
	return &stubService{store: make(map[string]transfer.View)}
}

func (s *stubService) Create(_ context.Context, _ string, req transfer.CreateRequest) (transfer.View, error) {
	if err := req.Validate(); err != nil {
		return transfer.View{}, err
	}
	view := transfer.View{
		ID:          uuid.NewString(),
		Reference:   req.Reference,
		Source:      req.Source,
		Destination: req.Destination,
		Amount:      req.Amount,
		Currency:    req.Currency,
		State:       transfer.StateDebited,
	}
	s.mu.Lock()
	s.store[view.ID] = view
	s.mu.Unlock()
	return view, nil
}

func (s *stubService) Get(_ context.Context, id string) (transfer.View, error) {
	s.mu.Lock()
	view, ok := s.store[id]
	s.mu.Unlock()
	if !ok {
		return transfer.View{}, transfer.ErrNotFound
	}
	return view, nil
}

// newTestServer wires the handler over the in-memory stub service.
func newTestServer() http.Handler {
	return NewHandler(newStubService()).Routes()
}

func doRequest(t *testing.T, srv http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

const validBody = `{"source":"CUST-001","destination":"CUST-002","amount_minor":5000,"currency":"NGN","reference":"ref-1"}`

func withKey() map[string]string { return map[string]string{idempotencyHeader: "key-1"} }

func TestCreateTransfer_Success(t *testing.T) {
	rec := doRequest(t, newTestServer(), http.MethodPost, "/v1/transfers", validBody, withKey())
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var resp transferResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID == "" {
		t.Error("expected a non-empty transfer id")
	}
	if resp.State != string(transfer.StateDebited) {
		t.Errorf("state = %q, want %q", resp.State, transfer.StateDebited)
	}
	if resp.AmountMinor.Minor() != 5000 {
		t.Errorf("amount_minor = %d, want 5000", resp.AmountMinor.Minor())
	}
}

func TestCreateTransfer_BadRequests(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		headers  map[string]string
		wantCode string
	}{
		{"missing idempotency key", validBody, nil, codeMissingIdemKey},
		{"non-positive amount", `{"source":"A","destination":"B","amount_minor":0,"currency":"NGN","reference":"r"}`, withKey(), codeValidation},
		{"unknown currency", `{"source":"A","destination":"B","amount_minor":100,"currency":"USD","reference":"r"}`, withKey(), codeValidation},
		{"missing field", `{"source":"","destination":"B","amount_minor":100,"currency":"NGN","reference":"r"}`, withKey(), codeValidation},
		{"invalid json", `{not json`, withKey(), codeValidation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doRequest(t, newTestServer(), http.MethodPost, "/v1/transfers", tt.body, tt.headers)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			var resp errorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if resp.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", resp.Code, tt.wantCode)
			}
			if resp.Message == "" {
				t.Error("expected a non-empty error message")
			}
		})
	}
}

// errService is a transfer.Service that always fails with a fixed error, so the
// transport's structured-error mapping can be exercised in isolation.
type errService struct{ err error }

func (s errService) Create(context.Context, string, transfer.CreateRequest) (transfer.View, error) {
	return transfer.View{}, s.err
}

func (s errService) Get(context.Context, string) (transfer.View, error) {
	return transfer.View{}, s.err
}

func TestCreateTransfer_StructuredErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"idempotency conflict", transfer.ErrIdempotencyConflict, http.StatusConflict, codeIdemConflict},
		{"in progress", transfer.ErrInProgress, http.StatusConflict, codeInProgress},
		{"backpressure", fmt.Errorf("post debit leg: %w", status.Error(codes.Unavailable, "ledger: serialization retries exhausted")), http.StatusServiceUnavailable, codeUnavailable},
		{"opaque internal", errors.New("some internal failure"), http.StatusInternalServerError, codeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewHandler(errService{err: tt.err}).Routes()
			rec := doRequest(t, srv, http.MethodPost, "/v1/transfers", validBody, withKey())
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			var resp errorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if resp.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", resp.Code, tt.wantCode)
			}
			if tt.wantStatus == http.StatusInternalServerError && resp.Message != "internal error" {
				t.Errorf("internal message = %q, want %q (no leak)", resp.Message, "internal error")
			}
			if tt.wantStatus == http.StatusServiceUnavailable && rec.Header().Get("Retry-After") == "" {
				t.Error("503 missing Retry-After header")
			}
		})
	}
}

// TestErrorBody_CarriesCorrelationID: the structured error echoes the request's
// correlation id so a caller can quote it when reporting a failure.
func TestErrorBody_CarriesCorrelationID(t *testing.T) {
	headers := map[string]string{"X-Correlation-ID": "corr-err-1"}
	rec := doRequest(t, newTestServer(), http.MethodPost, "/v1/transfers", validBody, headers)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (missing idempotency key)", rec.Code)
	}
	var resp errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.CorrelationID != "corr-err-1" {
		t.Errorf("correlation_id = %q, want %q", resp.CorrelationID, "corr-err-1")
	}
}

func TestGetTransfer_RoundTrip(t *testing.T) {
	srv := newTestServer()

	create := doRequest(t, srv, http.MethodPost, "/v1/transfers", validBody, withKey())
	var created transferResponse
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	get := doRequest(t, srv, http.MethodGet, "/v1/transfers/"+created.ID, "", nil)
	if get.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", get.Code, get.Body.String())
	}
	var fetched transferResponse
	if err := json.Unmarshal(get.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if fetched.ID != created.ID {
		t.Errorf("id = %q, want %q", fetched.ID, created.ID)
	}
}

func TestGetTransfer_NotFound(t *testing.T) {
	rec := doRequest(t, newTestServer(), http.MethodGet, "/v1/transfers/does-not-exist", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}
