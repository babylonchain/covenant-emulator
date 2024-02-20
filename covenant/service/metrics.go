package service

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

type PrometheusServer struct {
	svr *http.Server

	logger *zap.Logger

	interval time.Duration

	quit chan struct{}
}

func CreatePrometheusServer(addr string, interval time.Duration, logger *zap.Logger) *PrometheusServer {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	return &PrometheusServer{
		svr: &http.Server{
			Handler:           mux,
			Addr:              addr,
			ReadTimeout:       1 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       30 * time.Second,
			ReadHeaderTimeout: 2 * time.Second,
		},
		interval: interval,
		logger:   logger,
		quit:     make(chan struct{}, 1),
	}
}

func (ps *PrometheusServer) Start() {
	ps.logger.Info("Starting Prometheus server",
		zap.String("address", ps.svr.Addr))

	go ps.StartMetrics()

	if err := ps.svr.ListenAndServe(); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			ps.logger.Info("Prometheus server shutdown complete")
			return
		}
		ps.logger.Fatal("failed to start Prometheus server",
			zap.Error(err))
	}
}

func (ps *PrometheusServer) Stop() {
	ps.logger.Info("Stopping Prometheus server")

	close(ps.quit)
	if err := ps.svr.Shutdown(context.Background()); err != nil {
		ps.logger.Error("failed to stop the Prometheus server",
			zap.Error(err))
		ps.logger.Info("force stopping the Prometheus server")
		if err = ps.svr.Close(); err != nil {
			ps.logger.Error("failed to force stopping the Prometheus server",
				zap.Error(err))
		}
	}
}

type MetricsTimer struct {
	mu                                              sync.Mutex
	previousSubmission                              *time.Time
	previousLocalSignStart, previousLocalSignFinish *time.Time
}

func newMetricsTimer() *MetricsTimer {
	return &MetricsTimer{
		mu: sync.Mutex{},
	}
}

func (mt *MetricsTimer) SetPreviousSubmission(t *time.Time) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	mt.previousSubmission = t
}

func (mt *MetricsTimer) SetPreviousLocalSignStart(t *time.Time) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	mt.previousLocalSignStart = t
}

func (mt *MetricsTimer) SetPreviousLocalSignFinish(t *time.Time) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	mt.previousLocalSignFinish = t
}

func (mt *MetricsTimer) UpdatePrometheusMetrics() {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	// only start updating metrics after the first submission is finished
	if mt.previousSubmission == nil || mt.previousLocalSignStart == nil || mt.previousLocalSignFinish == nil {
		return
	}

	// Update Prometheus Gauges
	SecondsSinceLastSubmission.Set(time.Since(*mt.previousSubmission).Seconds())
	SecondsSinceLastSignStart.Set(time.Since(*mt.previousLocalSignStart).Seconds())
	SecondsSinceLastSignFinish.Set(time.Since(*mt.previousLocalSignFinish).Seconds())
}

var (
	// Variables to calculate Prometheus Metrics
	metricsTimeKeeper = newMetricsTimer()

	// Prometheus metrics
	TotalSignDelegationsSubmitted = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ce_total_sign_delegations_submitted",
			Help: "Total number of signed delegations submitted",
		},
		[]string{"covenant_pk"},
	)
	FailedSignDelegations = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ce_total_failed_sign_delegations",
			Help: "Total number of failed sign delegations",
		},
		[]string{"covenant_pk"},
	)
	CurrentPendingDelegations = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ce_current_pending_delegations",
			Help: "The number of current pending delegations",
		},
		[]string{"covenant_pk"},
	)
	SecondsSinceLastSubmission = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ce_seconds_since_last_submission",
		Help: "Seconds since last submission of signatures",
	})
	SecondsSinceLastSignStart = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ce_seconds_since_last_sign_start_time",
		Help: "Seconds since last sign start",
	})
	SecondsSinceLastSignFinish = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ce_seconds_since_last_sign_finish_time",
		Help: "Seconds since last sign finish",
	})

	TimedSignDelegationLag = promauto.NewSummary(prometheus.SummaryOpts{
		Name:       "ce_sign_delegation_lag_seconds",
		Help:       "Seconds taken to sign a delegation",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
	})
)

func (ps *PrometheusServer) StartMetrics() {
	// Update elapsed times on an interval basis
	ps.logger.Info("starting metrics update loop",
		zap.Duration("interval seconds", ps.interval))
	for {
		metricsTimeKeeper.UpdatePrometheusMetrics()

		select {
		case <-time.After(ps.interval):
		case <-ps.quit:
			ps.logger.Info("exiting metrics update loop")
			return
		}
	}
}
