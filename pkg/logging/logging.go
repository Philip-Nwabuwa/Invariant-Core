// Package logging provides a structured slog logger whose every line carries a
// correlation_id when one is present on the context. The id is propagated from
// the transfer request through rail callbacks and ledger calls (ARCHITECTURE §7).
package logging

import (
	"context"
	"log/slog"
	"os"
)

// CorrelationIDKey is the log attribute (and conventional header) name.
const CorrelationIDKey = "correlation_id"

type ctxKey struct{}

// New returns a JSON slog.Logger at the given level that automatically attaches
// the context correlation_id to each record.
func New(level slog.Level) *slog.Logger {
	base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	return slog.New(&correlationHandler{Handler: base})
}

// ContextWithCorrelationID returns a context carrying the given correlation id.
func ContextWithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// CorrelationID returns the correlation id on the context, or "" if absent.
func CorrelationID(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// correlationHandler decorates records with the context correlation_id.
type correlationHandler struct {
	slog.Handler
}

func (h *correlationHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := CorrelationID(ctx); id != "" {
		r.AddAttrs(slog.String(CorrelationIDKey, id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h *correlationHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &correlationHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *correlationHandler) WithGroup(name string) slog.Handler {
	return &correlationHandler{Handler: h.Handler.WithGroup(name)}
}
