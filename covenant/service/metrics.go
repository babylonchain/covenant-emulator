package service

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

type PrometheusServer struct {
	svr    *http.Server
	logger *zap.Logger
}

func CreatePrometheusServer(addr string, logger *zap.Logger) *PrometheusServer {
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
		logger: logger,
	}
}

func (ps *PrometheusServer) Start() {
	ps.logger.Info("starting Prometheus server",
		zap.String("address", ps.svr.Addr))
	if err := ps.svr.ListenAndServe(); err != nil {
		ps.logger.Fatal("failed to start Prometheus server",
			zap.Error(err))
	}
}

func (ps *PrometheusServer) Stop() {
	ps.logger.Info("stopping Prometheus server")
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
