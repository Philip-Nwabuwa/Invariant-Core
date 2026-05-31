package transfer

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/metrics"
)

// Metrics holds the switch's Prometheus instruments (NS-308, NFR-7): the
// terminal-outcome split, reversal latency, the outbox backlog, and the
// dead-letter count. Every method is nil-safe so the driver and poller can be
// built without metrics in tests.
type Metrics struct {
	outcomes        *prometheus.CounterVec
	reversalLatency prometheus.Histogram
	outboxLag       prometheus.Gauge
	deadLetters     prometheus.Counter
}

// NewMetrics registers the switch instruments on reg and returns them. The
// outcome series are pre-initialised to zero so each terminal state is present
// on /metrics before the first transfer completes.
func NewMetrics(reg *metrics.Registry) *Metrics {
	m := &Metrics{
		outcomes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "switch_transfer_outcomes_total",
			Help: "Transfers that reached a terminal state, by outcome.",
		}, []string{"outcome"}),
		reversalLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "switch_reversal_latency_seconds",
			Help:    "Time from transfer initiation to a completed reversal.",
			Buckets: prometheus.DefBuckets,
		}),
		outboxLag: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "switch_outbox_lag_seconds",
			Help: "Age in seconds of the oldest unpublished outbox event (0 when drained).",
		}),
		deadLetters: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "switch_outbox_dead_letters_total",
			Help: "Outbox events parked at the dead-letter terminus.",
		}),
	}
	reg.MustRegister(m.outcomes, m.reversalLatency, m.outboxLag, m.deadLetters)
	for _, o := range []string{statusSettled, statusReversed, statusManualReview, statusFailed} {
		m.outcomes.WithLabelValues(o)
	}
	return m
}

// recordOutcome counts a transfer reaching the given terminal outcome. The label
// is the coarse terminal status (settled / reversed / manual_review / failed).
func (m *Metrics) recordOutcome(outcome string) {
	if m != nil {
		m.outcomes.WithLabelValues(outcome).Inc()
	}
}

// observeReversalLatency records how long a reversal took end to end.
func (m *Metrics) observeReversalLatency(d time.Duration) {
	if m != nil {
		m.reversalLatency.Observe(d.Seconds())
	}
}

// SetOutboxLag publishes the current outbox backlog age (seconds).
func (m *Metrics) SetOutboxLag(seconds float64) {
	if m != nil {
		m.outboxLag.Set(seconds)
	}
}

// IncDeadLetter counts an event parked at the dead-letter terminus.
func (m *Metrics) IncDeadLetter() {
	if m != nil {
		m.deadLetters.Inc()
	}
}
