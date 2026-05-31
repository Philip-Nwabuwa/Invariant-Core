package transport

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/logging"
)

// probe captures the correlation id the middleware placed on the request
// context, so the test can assert it matches the response header.
func newCorrelationRouter(seen *string) http.Handler {
	r := chi.NewRouter()
	r.Use(correlationID)
	r.Get("/x", func(w http.ResponseWriter, req *http.Request) {
		*seen = logging.CorrelationID(req.Context())
		w.WriteHeader(http.StatusOK)
	})
	return r
}

// TestCorrelationMiddleware_GeneratesWhenAbsent: with no incoming header, the
// middleware generates an id, puts it on the context, and echoes it on the
// response.
func TestCorrelationMiddleware_GeneratesWhenAbsent(t *testing.T) {
	var seen string
	rec := httptest.NewRecorder()
	newCorrelationRouter(&seen).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	echoed := rec.Header().Get(logging.CorrelationHeader)
	if echoed == "" {
		t.Fatal("response missing generated correlation id header")
	}
	if seen != echoed {
		t.Errorf("context id %q != response header %q", seen, echoed)
	}
}

// TestCorrelationMiddleware_PreservesIncoming: an incoming correlation id is
// preserved on the context and echoed unchanged.
func TestCorrelationMiddleware_PreservesIncoming(t *testing.T) {
	var seen string
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(logging.CorrelationHeader, "corr-abc")
	rec := httptest.NewRecorder()
	newCorrelationRouter(&seen).ServeHTTP(rec, req)

	if seen != "corr-abc" {
		t.Errorf("context id = %q, want corr-abc", seen)
	}
	if echoed := rec.Header().Get(logging.CorrelationHeader); echoed != "corr-abc" {
		t.Errorf("response header = %q, want corr-abc", echoed)
	}
}
