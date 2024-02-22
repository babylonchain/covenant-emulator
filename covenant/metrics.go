package covenant

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type metricsTimer struct {
	mu                                              sync.Mutex
	previousSubmission                              *time.Time
	previousLocalSignStart, previousLocalSignFinish *time.Time
}

func newMetricsTimer() *metricsTimer {
	return &metricsTimer{
		mu: sync.Mutex{},
	}
}

func (mt *metricsTimer) SetPreviousSubmission(t *time.Time) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	mt.previousSubmission = t
}

func (mt *metricsTimer) SetPreviousSignStart(t *time.Time) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	mt.previousLocalSignStart = t
}

func (mt *metricsTimer) SetPreviousSignFinish(t *time.Time) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	mt.previousLocalSignFinish = t
}

func (mt *metricsTimer) UpdatePrometheusMetrics() {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	// only start updating metrics after the first submission is finished
	if mt.previousSubmission == nil || mt.previousLocalSignStart == nil || mt.previousLocalSignFinish == nil {
		return
	}

	// Update Prometheus Gauges
	secondsSinceLastSubmission.Set(time.Since(*mt.previousSubmission).Seconds())
	secondsSinceLastSignStart.Set(time.Since(*mt.previousLocalSignStart).Seconds())
	secondsSinceLastSignFinish.Set(time.Since(*mt.previousLocalSignFinish).Seconds())
}

var (
	// Variables to calculate Prometheus Metrics
	metricsTimeKeeper = newMetricsTimer()

	// Prometheus metrics
	totalSignDelegationsSubmitted = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ce_total_sign_delegations_submitted",
			Help: "Total number of signed delegations submitted",
		},
		[]string{"covenant_pk"},
	)
	failedSignDelegations = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ce_total_failed_sign_delegations",
			Help: "Total number of failed sign delegations",
		},
		[]string{"covenant_pk"},
	)
	currentPendingDelegations = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "ce_current_pending_delegations",
			Help: "The number of current pending delegations",
		},
		[]string{"covenant_pk"},
	)
	secondsSinceLastSubmission = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ce_seconds_since_last_submission",
		Help: "Seconds since last submission of signatures",
	})
	secondsSinceLastSignStart = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ce_seconds_since_last_sign_start_time",
		Help: "Seconds since last sign start",
	})
	secondsSinceLastSignFinish = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ce_seconds_since_last_sign_finish_time",
		Help: "Seconds since last sign finish",
	})

	timedSignDelegationLag = promauto.NewSummary(prometheus.SummaryOpts{
		Name:       "ce_sign_delegation_lag_seconds",
		Help:       "Seconds taken to sign a delegation",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
	})
)
