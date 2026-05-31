package transport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
)

// newTestServer wires the handler over a real (in-memory) stub service.
func newTestServer() http.Handler {
	return NewHandler(transfer.NewStubService()).Routes()
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
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp transferResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID == "" {
		t.Error("expected a non-empty transfer id")
	}
	if resp.State != string(transfer.StateSettled) {
		t.Errorf("state = %q, want %q", resp.State, transfer.StateSettled)
	}
	if resp.AmountMinor.Minor() != 5000 {
		t.Errorf("amount_minor = %d, want 5000", resp.AmountMinor.Minor())
	}
}

func TestCreateTransfer_BadRequests(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		headers map[string]string
	}{
		{"missing idempotency key", validBody, nil},
		{"non-positive amount", `{"source":"A","destination":"B","amount_minor":0,"currency":"NGN","reference":"r"}`, withKey()},
		{"unknown currency", `{"source":"A","destination":"B","amount_minor":100,"currency":"USD","reference":"r"}`, withKey()},
		{"missing field", `{"source":"","destination":"B","amount_minor":100,"currency":"NGN","reference":"r"}`, withKey()},
		{"invalid json", `{not json`, withKey()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doRequest(t, newTestServer(), http.MethodPost, "/v1/transfers", tt.body, tt.headers)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
		})
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
