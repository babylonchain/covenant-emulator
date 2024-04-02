package service

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

type PrometheusServer struct {
	svr *http.Server

	logger *zap.Logger

	interval time.Duration

	quit chan struct{}
}

func NewPrometheusServer(addr string, interval time.Duration, logger *zap.Logger) *PrometheusServer {
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

	if err := ps.svr.ListenAndServe(); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			// the Prometheus server is shutdown
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
