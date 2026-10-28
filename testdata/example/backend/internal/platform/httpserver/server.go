// Package httpserver runs the backend's HTTP transport with shared health,
// middleware, and graceful shutdown.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// Server provides the HTTP transport.
type Server struct {
	logger          *slog.Logger
	shutdownTimeout time.Duration
	// ready is true while the transport accepts work; checksOK reflects the
	// latest readiness checks. Both must hold for the backend to be ready.
	ready             atomic.Bool
	checksOK          atomic.Bool
	checks            []readinessCheck
	readinessInterval time.Duration
	mux               *http.ServeMux
	http              *http.Server
	onShutdown        []func()
}

type readinessCheck struct {
	name  string
	check func(context.Context) error
}

const readinessCheckTimeout = 2 * time.Second

// New creates a server with /healthz and /readyz. Register routes with
// Handle before Serve.
func New(logger *slog.Logger, shutdownTimeout time.Duration) *Server {
	s := &Server{
		logger:            logger,
		shutdownTimeout:   shutdownTimeout,
		readinessInterval: 5 * time.Second,
	}
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

// Handler returns the HTTP handler with middleware, for tests.
func (s *Server) Handler() http.Handler { return s.http.Handler }

// Logger returns the backend logger.
func (s *Server) Logger() *slog.Logger { return s.logger }

// AddReadinessCheck registers a check for a critical dependency such as a
// database. Checks run before Serve reports readiness and periodically after;
// while any fails, /readyz returns 503. Liveness (/healthz) never depends on
// checks.
func (s *Server) AddReadinessCheck(name string, check func(context.Context) error) {
	s.checks = append(s.checks, readinessCheck{name: name, check: check})
}

func (s *Server) isReady() bool { return s.ready.Load() && s.checksOK.Load() }

// runChecks evaluates every readiness check and logs changes.
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
}

// OnShutdown registers f to run after the transport has stopped, such as
// closing a database pool. Functions run in reverse order.
func (s *Server) OnShutdown(f func()) {
	s.onShutdown = append(s.onShutdown, f)
}

// Serve owns the listener and drains the transport when ctx is canceled.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	defer listener.Close()
	serveErr := make(chan error, 1)
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
	go func() { serveErr <- s.http.Serve(listener) }()
	s.logger.Info("server started", "http_addr", listener.Addr().String())

	var err error
	served := false
	select {
	case <-ctx.Done():
	case err = <-serveErr:
		served = true
	}
	s.ready.Store(false)
	close(stopChecks)
	<-checksDone
	s.logger.Info("server shutting down")
	// http.Server.Shutdown treats connections that have not sent a request
	// (such as browser preconnects) as idle only after about 5s, so the
	// shutdown timeout should stay well above that.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()
	shutdownErr := s.http.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		_ = s.http.Close()
	}
	// Serve returns once the server is stopped.
	if !served {
		err = <-serveErr
	}
	for i := len(s.onShutdown) - 1; i >= 0; i-- {
		s.onShutdown[i]()
	}
	var errs []error
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		errs = append(errs, fmt.Errorf("serve: %w", err))
	}
	if shutdownErr != nil {
		errs = append(errs, fmt.Errorf("shutdown HTTP: %w", shutdownErr))
	}
	return errors.Join(errs...)
}

func writeStatus(w http.ResponseWriter, code int, status string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, "{\"status\":%q}\n", status)
}
