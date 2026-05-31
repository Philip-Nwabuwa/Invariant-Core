// Package health provides the small HTTP server every service runs to expose
// liveness at /healthz and Prometheus metrics at /metrics (ROADMAP NS-004/005).
package health

import (
	"encoding/json"
	"net/http"
	"time"
)

// NewServer builds an *http.Server listening on addr that serves /healthz
// (200 JSON {"status":"ok"}) and, when metrics is non-nil, /metrics. The caller
// owns the lifecycle (ListenAndServe / Shutdown).
//
// register, when non-nil, is called with the mux after /healthz and /metrics
// are mounted, so a service can attach its own routes (e.g. switchd's REST API)
// on the same listener without disturbing health/metrics.
func NewServer(addr string, metrics http.Handler, register func(*http.ServeMux)) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handleHealthz)
	if metrics != nil {
		mux.Handle("/metrics", metrics)
	}
	if register != nil {
		register(mux)
	}
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
