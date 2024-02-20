package service

import (
	"fmt"
	"sync/atomic"

	"github.com/lightningnetwork/lnd/signal"
	"go.uber.org/zap"

	"github.com/babylonchain/covenant-emulator/covenant"
)

// CovenantServer is the main daemon construct for the covenant emulator.
type CovenantServer struct {
	started int32

	ce *covenant.CovenantEmulator

	logger *zap.Logger

	interceptor signal.Interceptor
}

// NewCovenantServer creates a new server with the given config.
func NewCovenantServer(l *zap.Logger, ce *covenant.CovenantEmulator, sig signal.Interceptor) *CovenantServer {
	return &CovenantServer{
		logger:      l,
		ce:          ce,
		interceptor: sig,
	}
}

// RunUntilShutdown runs the main EOTS manager server loop until a signal is
// received to shut down the process.
func (s *CovenantServer) RunUntilShutdown() error {
	if atomic.AddInt32(&s.started, 1) != 1 {
		return nil
	}

	metricsCfg := s.ce.Config().Metrics
	promAddr, err := metricsCfg.Address()
	if err != nil {
		return err
	}

	ps := NewPrometheusServer(promAddr, metricsCfg.UpdateInterval, s.logger)

	defer func() {
		ps.Stop()
		s.logger.Info("Shutdown Prometheus server complete")
		_ = s.ce.Stop()
		s.logger.Info("Shutdown covenant emulator server complete")
	}()

	go ps.Start()

	if err := s.ce.Start(); err != nil {
		return fmt.Errorf("failed to start covenant emulator: %w", err)
	}

	s.logger.Info("Covenant Emulator Daemon is fully active!")

	// Wait for shutdown signal from either a graceful server stop or from
	// the interrupt handler.
	<-s.interceptor.ShutdownChannel()

	return nil
}
