package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/Ceferinto/habbyte/backend/internal/platform/httpserver"
)

func TestProbeAddress(t *testing.T) {
	for in, want := range map[string]string{
		":8080":          "127.0.0.1:8080",
		"0.0.0.0:8080":   "127.0.0.1:8080",
		"[::]:9090":      "[::1]:9090",
		"10.0.0.5:9090":  "10.0.0.5:9090",
		"localhost:8080": "localhost:8080",
	} {
		got, err := probeAddress(in)
		if err != nil || got != want {
			t.Errorf("probeAddress(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := probeAddress("8080"); err == nil {
		t.Error("probeAddress accepted an address without a port")
	}
}

func TestHealthcheck(t *testing.T) {
	httpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HTTP_ADDR", httpListener.Addr().String())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- httpserver.New(discardLogger(), 5*time.Second).Serve(ctx, httpListener)
	}()
	if err := healthcheck(); err != nil {
		t.Fatalf("healthcheck on a running server: %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := healthcheck(); err == nil {
		t.Fatal("healthcheck passed after shutdown")
	}
}

func TestRunRegisterError(t *testing.T) {
	t.Setenv("HTTP_ADDR", "127.0.0.1:0")
	want := errors.New("bad registration")
	err := run(context.Background(), discardLogger(), func(context.Context, *httpserver.Server) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("run error = %v, want %v", err, want)
	}
}

func TestRunInvalidShutdownTimeout(t *testing.T) {
	t.Setenv("SHUTDOWN_TIMEOUT", "soon")
	if err := run(context.Background(), discardLogger(), nil); err == nil {
		t.Fatal("run accepted an invalid SHUTDOWN_TIMEOUT")
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
