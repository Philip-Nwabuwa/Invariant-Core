// Package transport holds the switch's public HTTP surface: the REST transfer
// API. It depends on the transfer.Service interface, never on the engine's
// internals, so the wire format and the domain logic evolve independently.
package transport

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/logging"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/money"
)

// idempotencyHeader is the required header that deduplicates retried transfers.
const idempotencyHeader = "Idempotency-Key"

// Handler serves the REST transfer API over a transfer.Service.
type Handler struct {
	svc transfer.Service
}

// NewHandler builds a Handler backed by svc.
func NewHandler(svc transfer.Service) *Handler {
	return &Handler{svc: svc}
}

// Routes returns the chi router for the transfer API. It is mounted onto the
// service's HTTP listener in cmd/switchd.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(correlationID)
	r.Post("/v1/transfers", h.create)
	r.Get("/v1/transfers/{id}", h.get)
	return r
}

// correlationID is chi middleware that establishes a correlation id for the
// request: it reuses an incoming X-Correlation-ID header or generates one, puts
// it on the request context (so logs and downstream gRPC calls carry it), and
// echoes it on the response so the caller can correlate too.
func correlationID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(logging.CorrelationHeader)
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set(logging.CorrelationHeader, id)
		ctx := logging.ContextWithCorrelationID(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// createTransferRequest is the JSON body of POST /v1/transfers. amount_minor
// uses money.Amount so it decodes as a bare integer of minor units (kobo).
type createTransferRequest struct {
	Source      string       `json:"source"`
	Destination string       `json:"destination"`
	AmountMinor money.Amount `json:"amount_minor"`
	Currency    string       `json:"currency"`
	Reference   string       `json:"reference"`
}

// transferResponse is the JSON view returned for a transfer.
type transferResponse struct {
	ID          string       `json:"id"`
	Reference   string       `json:"reference"`
	Source      string       `json:"source"`
	Destination string       `json:"destination"`
	AmountMinor money.Amount `json:"amount_minor"`
	Currency    string       `json:"currency"`
	State       string       `json:"state"`
}

// errorResponse is the structured JSON body of an error (NS-506): a stable
// machine-readable code, a human-readable message, and the request's correlation
// id so a caller can quote it when reporting a problem.
type errorResponse struct {
	Code          string `json:"code"`
	Message       string `json:"message"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get(idempotencyHeader)
	if key == "" {
		writeError(w, r, transfer.ErrMissingIdempotencyKey)
		return
	}

	var body createTransferRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, r, errInvalidJSONBody)
		return
	}

	req := transfer.CreateRequest{
		Source:      body.Source,
		Destination: body.Destination,
		Amount:      body.AmountMinor,
		Currency:    body.Currency,
		Reference:   body.Reference,
	}

	view, err := h.svc.Create(r.Context(), key, req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// 202 Accepted: the transfer is durably accepted and debited, but settlement
	// completes asynchronously via the outbox. Clients poll GET until a terminal
	// state (SETTLED / REVERSED / FAILED / MANUAL_REVIEW).
	writeJSON(w, http.StatusAccepted, toResponse(view))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	view, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toResponse(view))
}

func toResponse(v transfer.View) transferResponse {
	return transferResponse{
		ID:          v.ID,
		Reference:   v.Reference,
		Source:      v.Source,
		Destination: v.Destination,
		AmountMinor: v.Amount,
		Currency:    v.Currency,
		State:       string(v.State),
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeError renders the structured error body (NS-506): it classifies err into
// a stable code + HTTP status, attaches the request's correlation id, and (for a
// transient 503) hints the client to retry. The message is the error text for
// client-actionable failures and a generic line for an opaque 500.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	ae := classify(err)
	msg := err.Error()
	if ae.status == http.StatusInternalServerError {
		msg = "internal error"
	}
	if ae.status == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", "1")
	}
	writeJSON(w, ae.status, errorResponse{
		Code:          ae.code,
		Message:       msg,
		CorrelationID: logging.CorrelationID(r.Context()),
	})
}
