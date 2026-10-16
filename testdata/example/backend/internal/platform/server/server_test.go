package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CMOT/habyta/backend/internal/platform/requestid"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestReadinessBeforeStart(t *testing.T) {
	s := New(discardLogger(), 5*time.Second)
	response := httptest.NewRecorder()
	s.http.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness before start = %d, want 503", response.Code)
	}
}

func TestTransportsAndShutdown(t *testing.T) {
	httpListener := listen(t)
	grpcListener := listen(t)
	s := New(discardLogger(), 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	var order []int
	s.OnShutdown(func() { order = append(order, 1) })
	s.OnShutdown(func() { order = append(order, 2) })
	go func() { done <- s.Serve(ctx, httpListener, grpcListener) }()
	client := &http.Client{Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := client.Get("http://" + httpListener.Addr().String() + path)
		if err != nil {
			t.Fatal(err)
		}
		// Read bodies fully so the client reuses its connection; an unused
		// fresh connection delays http.Server.Shutdown by about 5s.
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("%s: unexpected response: %s", path, resp.Status)
		}
	}
	conn, err := grpc.NewClient(grpcListener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	checkCtx, checkCancel := context.WithTimeout(ctx, 3*time.Second)
	defer checkCancel()
	resp, err := healthpb.NewHealthClient(conn).Check(checkCtx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("gRPC health = %s", resp.GetStatus())
	}
	client.CloseIdleConnections()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not complete")
	}
	if len(order) != 2 || order[0] != 2 || order[1] != 1 {
		t.Fatalf("shutdown hooks ran in order %v, want [2 1]", order)
	}
	response := httptest.NewRecorder()
	s.http.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness after shutdown = %d, want 503", response.Code)
	}
	health, err := s.health.Check(context.Background(), &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if health.GetStatus() != healthpb.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("gRPC health after shutdown = %s, want NOT_SERVING", health.GetStatus())
	}
}

func TestServeReturnsTransportError(t *testing.T) {
	httpListener := listen(t)
	grpcListener := listen(t)
	httpListener.Close()
	s := New(discardLogger(), 5*time.Second)
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), httpListener, grpcListener) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Serve returned nil after the HTTP listener failed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not stop after a transport failed")
	}
	if s.ready.Load() {
		t.Fatal("server still ready after a transport failed")
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	return listener
}

func TestHandleRegistersRouteWithMiddleware(t *testing.T) {
	s := New(discardLogger(), 5*time.Second)
	s.Handle("GET /ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	s.http.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ping", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", response.Code)
	}
	if response.Header().Get(requestid.Header) == "" {
		t.Fatal("registered route bypassed middleware")
	}
}

func TestReadinessChecks(t *testing.T) {
	s := New(discardLogger(), 5*time.Second)
	s.readinessInterval = 10 * time.Millisecond
	var failing atomic.Bool
	failing.Store(true)
	s.AddReadinessCheck("db", func(context.Context) error {
		if failing.Load() {
			return errors.New("down")
		}
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, listen(t), listen(t)) }()

	waitFor(t, "not ready while check fails", func() bool {
		return readyz(s) == http.StatusServiceUnavailable && healthStatus(t, s) == healthpb.HealthCheckResponse_NOT_SERVING
	})
	failing.Store(false)
	waitFor(t, "ready once check passes", func() bool {
		return readyz(s) == http.StatusOK && healthStatus(t, s) == healthpb.HealthCheckResponse_SERVING
	})
	failing.Store(true)
	waitFor(t, "not ready after check fails again", func() bool {
		return readyz(s) == http.StatusServiceUnavailable && healthStatus(t, s) == healthpb.HealthCheckResponse_NOT_SERVING
	})
	// Liveness never depends on checks.
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("liveness = %d while a readiness check fails", response.Code)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func readyz(s *Server) int {
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	return response.Code
}

func healthStatus(t *testing.T, s *Server) healthpb.HealthCheckResponse_ServingStatus {
	t.Helper()
	resp, err := s.health.Check(context.Background(), &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return resp.GetStatus()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
