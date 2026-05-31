// Package transport holds the switch's public HTTP surface: the REST transfer
// API. It depends on the transfer.Service interface, never on the engine's
// internals, so the wire format and the domain logic evolve independently.
package transport

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	transfer "github.com/Philip-Nwabuwa/Invariant-Core/internal/switch"
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
	r.Post("/v1/transfers", h.create)
	r.Get("/v1/transfers/{id}", h.get)
	return r
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

// errorResponse is the JSON body of an error.
type errorResponse struct {
	Error string `json:"error"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get(idempotencyHeader)
	if key == "" {
		writeError(w, http.StatusBadRequest, transfer.ErrMissingIdempotencyKey)
		return
	}

	var body createTransferRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid JSON body"))
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
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusCreated, toResponse(view))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	view, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeError(w, statusFor(err), err)
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

// statusFor maps a domain error to an HTTP status code. Unknown errors are 500.
func statusFor(err error) int {
	switch {
	case errors.Is(err, transfer.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, transfer.ErrMissingIdempotencyKey),
		errors.Is(err, transfer.ErrNonPositiveAmount),
		errors.Is(err, transfer.ErrUnknownCurrency),
		errors.Is(err, transfer.ErrMissingField):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, errorResponse{Error: err.Error()})
}
