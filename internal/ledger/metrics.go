package ledger

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/metrics"
)

// Metrics holds the ledger's serialization-retry SLIs (NS-505, ADR-0002). The
// SERIALIZABLE write path retries on SQLSTATE 40001 under contention; ADR-0002
// names that retry rate a first-class SLI because the shared SETTLEMENT suspense
// account is the hotspot that determines whether the throughput target is
// reachable. Every method is nil-safe so the service can be built without
// metrics in tests.
type Metrics struct {
	retries   prometheus.Counter
	exhausted prometheus.Counter
}

// NewMetrics registers the ledger instruments on reg and returns them.
func NewMetrics(reg *metrics.Registry) *Metrics {
	m := &Metrics{
		retries: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "ledger_serialization_retries_total",
			Help: "Serialization failures (SQLSTATE 40001) retried on the SERIALIZABLE write path.",
		}),
		exhausted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "ledger_serialization_exhausted_total",
			Help: "Posts that exhausted the bounded serialization-retry budget (surfaced as backpressure).",
		}),
	}
	reg.MustRegister(m.retries, m.exhausted)
	return m
}

// incRetry counts one serialization-failure retry.
func (m *Metrics) incRetry() {
	if m != nil {
		m.retries.Inc()
	}
}

// incExhausted counts one post that ran out of retry budget.
func (m *Metrics) incExhausted() {
	if m != nil {
		m.exhausted.Inc()
	}
}
