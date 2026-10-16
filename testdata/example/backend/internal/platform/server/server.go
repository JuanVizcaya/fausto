// Package server runs a service's HTTP and gRPC transports with shared
// health, middleware, interceptors, and graceful shutdown.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
)

// Server provides an HTTP transport and an internal gRPC transport.
type Server struct {
	logger          *slog.Logger
	shutdownTimeout time.Duration
	// ready is true while the transports accept work; checksOK reflects the
	// latest readiness checks. Both must hold for the service to be ready.
	ready             atomic.Bool
	checksOK          atomic.Bool
	checks            []readinessCheck
	readinessInterval time.Duration
	mux               *http.ServeMux
	http              *http.Server
	grpc              *grpc.Server
	health            *health.Server
	onShutdown        []func()
}

type readinessCheck struct {
	name  string
	check func(context.Context) error
}

const readinessCheckTimeout = 2 * time.Second

var _ grpc.ServiceRegistrar = (*Server)(nil)

// New creates a server with /healthz, /readyz, and the gRPC health service.
// Register routes and services with Handle and RegisterService before Serve.
func New(logger *slog.Logger, shutdownTimeout time.Duration) *Server {
	s := &Server{
		logger:            logger,
		shutdownTimeout:   shutdownTimeout,
		readinessInterval: 5 * time.Second,
		grpc: grpc.NewServer(
			grpc.ChainUnaryInterceptor(unaryLogging(logger), unaryRecovery(logger)),
			grpc.ChainStreamInterceptor(streamRecovery(logger)),
			grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
				MinTime:             10 * time.Second,
				PermitWithoutStream: true,
			}),
			// Recycling connections lets clients re-resolve DNS and spread load
			// across replicas added after the connection was opened.
			grpc.KeepaliveParams(keepalive.ServerParameters{
				MaxConnectionAge:      5 * time.Minute,
				MaxConnectionAgeGrace: 30 * time.Second,
			}),
		),
		health: health.NewServer(),
	}
	s.health.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	healthpb.RegisterHealthServer(s.grpc, s.health)
	mux := http.NewServeMux()
	s.mux = mux
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeStatus(w, http.StatusOK, "ok")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if !s.isReady() {
			writeStatus(w, http.StatusServiceUnavailable, "not_ready")
			return
		}
		writeStatus(w, http.StatusOK, "ready")
	})
	s.http = &http.Server{
		Handler:           withMiddleware(logger, mux),
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		// WriteTimeout applies to every response. Streaming or large-download
		// handlers must extend it with http.ResponseController.SetWriteDeadline.
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return s
}

// Handle registers an HTTP handler; see http.ServeMux for pattern syntax.
func (s *Server) Handle(pattern string, handler http.Handler) {
	s.mux.Handle(pattern, handler)
}

// RegisterService registers a gRPC service implementation. It makes Server
// usable with generated Register<Name>Server functions.
func (s *Server) RegisterService(desc *grpc.ServiceDesc, impl any) {
	s.grpc.RegisterService(desc, impl)
}

// Handler returns the HTTP handler with middleware, for tests.
func (s *Server) Handler() http.Handler { return s.http.Handler }

// Logger returns the service logger.
func (s *Server) Logger() *slog.Logger { return s.logger }

// AddReadinessCheck registers a check for a critical dependency such as a
// database. Checks run before Serve reports readiness and periodically after;
// while any fails, /readyz returns 503 and gRPC health reports NOT_SERVING.
// Liveness (/healthz) never depends on checks.
func (s *Server) AddReadinessCheck(name string, check func(context.Context) error) {
	s.checks = append(s.checks, readinessCheck{name: name, check: check})
}

func (s *Server) isReady() bool { return s.ready.Load() && s.checksOK.Load() }

// runChecks evaluates every readiness check, logs changes, and updates the
// gRPC health status to match.
func (s *Server) runChecks(ctx context.Context) {
	if ctx.Err() != nil {
		return // shutting down; failures would be noise
	}
	ok := true
	for _, c := range s.checks {
		checkCtx, cancel := context.WithTimeout(ctx, readinessCheckTimeout)
		err := c.check(checkCtx)
		cancel()
		if err != nil {
			ok = false
			s.logger.Warn("readiness check failed", "check", c.name, "error", err)
		}
	}
	if previous := s.checksOK.Swap(ok); previous != ok && ok {
		s.logger.Info("readiness checks passing")
	}
	if s.isReady() {
		s.health.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	} else {
		s.health.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	}
}

// OnShutdown registers f to run after both transports have stopped, such as
// closing outgoing gRPC connections. Functions run in reverse order.
func (s *Server) OnShutdown(f func()) {
	s.onShutdown = append(s.onShutdown, f)
}

// Serve owns the listeners and drains both transports when ctx is canceled.
func (s *Server) Serve(ctx context.Context, httpListener, grpcListener net.Listener) error {
	defer httpListener.Close()
	defer grpcListener.Close()
	errorsCh := make(chan error, 2)
	s.ready.Store(true)
	s.runChecks(ctx)
	stopChecks := make(chan struct{})
	checksDone := make(chan struct{})
	go func() {
		defer close(checksDone)
		ticker := time.NewTicker(s.readinessInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stopChecks:
				return
			case <-ticker.C:
				s.runChecks(ctx)
			}
		}
	}()
	go func() { errorsCh <- s.http.Serve(httpListener) }()
	go func() { errorsCh <- s.grpc.Serve(grpcListener) }()
	s.logger.Info("server started", "http_addr", httpListener.Addr().String(), "grpc_addr", grpcListener.Addr().String())

	var serveErrs []error
	pending := 2
	select {
	case <-ctx.Done():
	case err := <-errorsCh:
		serveErrs = append(serveErrs, err)
		pending--
	}
	s.ready.Store(false)
	close(stopChecks)
	<-checksDone
	s.health.Shutdown()
	s.logger.Info("server shutting down")
	// http.Server.Shutdown treats connections that have not sent a request
	// (such as browser preconnects) as idle only after about 5s, so the
	// shutdown timeout should stay well above that.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()
	grpcDone := make(chan struct{})
	go func() {
		s.grpc.GracefulStop()
		close(grpcDone)
	}()
	httpErr := s.http.Shutdown(shutdownCtx)
	if httpErr != nil {
		_ = s.http.Close()
	}
	select {
	case <-grpcDone:
	case <-shutdownCtx.Done():
		s.grpc.Stop()
		<-grpcDone
	}
	// Both Serve calls return once their server is stopped.
	for ; pending > 0; pending-- {
		serveErrs = append(serveErrs, <-errorsCh)
	}
	for i := len(s.onShutdown) - 1; i >= 0; i-- {
		s.onShutdown[i]()
	}
	var errs []error
	for _, err := range serveErrs {
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, grpc.ErrServerStopped) {
			errs = append(errs, fmt.Errorf("serve: %w", err))
		}
	}
	if httpErr != nil {
		errs = append(errs, fmt.Errorf("shutdown HTTP: %w", httpErr))
	}
	return errors.Join(errs...)
}

func writeStatus(w http.ResponseWriter, code int, status string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, "{\"status\":%q}\n", status)
}
