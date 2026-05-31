// Package serviceboot holds the common boot sequence shared by the long-running
// services (ledger, switchd, mockrail): structured logging, tracing, a Prometheus
// registry, a /healthz+/metrics server, an optional gRPC listener with an empty
// surface, and graceful shutdown on SIGINT/SIGTERM.
//
// Sprint 0: services boot and serve /healthz with no business logic. Real gRPC
// and REST surfaces are registered in later sprints.
package serviceboot

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"

	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/health"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/logging"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/metrics"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/tracing"
)

// Options configures a service boot.
type Options struct {
	// ServiceName labels logs and traces.
	ServiceName string
	// HealthAddr is the listen address for /healthz and /metrics (required).
	HealthAddr string
	// GRPCAddr, if non-empty, starts a *grpc.Server on that address.
	GRPCAddr string
	// RegisterGRPC, if set, is called with the gRPC server after it is created
	// and before it serves, so callers can register their service surface. When
	// nil the server starts with an empty surface (Sprint 0 behaviour).
	RegisterGRPC func(*grpc.Server)
	// RegisterHTTP, if set, is called with the health server's mux after
	// /healthz and /metrics are mounted, so a service can attach extra routes
	// (e.g. switchd's public REST API) on the same HealthAddr listener.
	RegisterHTTP func(*http.ServeMux)
	// Cleanup, if set, runs during graceful shutdown (e.g. closing a DB pool).
	Cleanup func()
}

// Run boots the service and blocks until a termination signal, then shuts
// everything down gracefully. It returns the first non-nil shutdown error.
func Run(opts Options) error {
	logger := logging.New(slog.LevelInfo)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := tracing.Init(ctx, opts.ServiceName, os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if err != nil {
		logger.Warn("tracing init failed; continuing without traces", "error", err)
	}

	reg := metrics.New()
	healthSrv := health.NewServer(opts.HealthAddr, reg.Handler(), opts.RegisterHTTP)

	serveErr := make(chan error, 2)
	go func() {
		if err := healthSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	var grpcSrv *grpc.Server
	if opts.GRPCAddr != "" {
		lis, err := net.Listen("tcp", opts.GRPCAddr)
		if err != nil {
			return err
		}
		// otelgrpc continues the trace from the caller (switch -> rail/ledger
		// becomes one trace); the correlation interceptor lifts the caller's
		// correlation id from metadata onto the handler context so server logs
		// carry it too.
		grpcSrv = grpc.NewServer(
			grpc.StatsHandler(otelgrpc.NewServerHandler()),
			grpc.ChainUnaryInterceptor(logging.UnaryServerInterceptor()),
		)
		if opts.RegisterGRPC != nil {
			opts.RegisterGRPC(grpcSrv)
		}
		go func() {
			if err := grpcSrv.Serve(lis); err != nil {
				serveErr <- err
			}
		}()
	}

	logger.Info("service started",
		"service", opts.ServiceName,
		"health_addr", opts.HealthAddr,
		"grpc_addr", opts.GRPCAddr,
	)

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received", "service", opts.ServiceName)
	case err := <-serveErr:
		logger.Error("server error; shutting down", "service", opts.ServiceName, "error", err)
	}

	return shutdown(healthSrv, grpcSrv, shutdownTracing, opts.Cleanup)
}

func shutdown(healthSrv *http.Server, grpcSrv *grpc.Server, shutdownTracing tracing.ShutdownFunc, cleanup func()) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var firstErr error
	if err := healthSrv.Shutdown(shutdownCtx); err != nil {
		firstErr = err
	}
	if grpcSrv != nil {
		grpcSrv.GracefulStop()
	}
	if cleanup != nil {
		cleanup()
	}
	if shutdownTracing != nil {
		if err := shutdownTracing(shutdownCtx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// EnvOr returns the environment value for key, or def when it is unset/empty.
func EnvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
