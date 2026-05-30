// Package metrics provides a per-service Prometheus registry and an HTTP handler
// to expose it (ARCHITECTURE §7). Services register their SLIs on the returned
// registry and mount Handler at /metrics.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry wraps a Prometheus registry seeded with the Go runtime and process
// collectors, so every service exports baseline metrics out of the box.
type Registry struct {
	*prometheus.Registry
}

// New returns a Registry with the default Go and process collectors registered.
func New() *Registry {
	r := prometheus.NewRegistry()
	r.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return &Registry{Registry: r}
}

// Handler returns an http.Handler that serves the registry in the Prometheus
// text exposition format.
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.Registry, promhttp.HandlerOpts{})
}
