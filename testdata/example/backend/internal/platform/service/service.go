// Package service is the shared process entry point for Habyta services:
// configuration, logging, signal handling, listeners, and the container
// healthcheck subcommand.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // scratch images have no zoneinfo; billing needs local time zones

	"github.com/CMOT/habyta/backend/internal/platform/server"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// RegisterFunc adds a service's HTTP routes, gRPC services, readiness
// checks, and shutdown hooks to s. ctx is canceled on SIGINT or SIGTERM, so
// startup work such as connecting to a database stops promptly.
type RegisterFunc func(ctx context.Context, s *server.Server) error

// Main runs the named service until SIGINT or SIGTERM, or runs the
// container healthcheck when invoked as "<binary> healthcheck".
// register may be nil. Main does not return.
func Main(name string, register RegisterFunc) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", name)
	if len(os.Args) > 1 {
		if len(os.Args) != 2 || os.Args[1] != "healthcheck" {
			logger.Error("usage: " + name + " [healthcheck]")
			os.Exit(1)
		}
		if err := healthcheck(); err != nil {
			logger.Error("healthcheck failed", "error", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, logger, register)
	stop()
	if err != nil {
		logger.Error("service stopped", "error", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func run(ctx context.Context, logger *slog.Logger, register RegisterFunc) error {
	shutdownTimeout, err := time.ParseDuration(Env("SHUTDOWN_TIMEOUT", "10s"))
	if err != nil || shutdownTimeout <= 0 {
		return fmt.Errorf("invalid SHUTDOWN_TIMEOUT %q", os.Getenv("SHUTDOWN_TIMEOUT"))
	}
	s := server.New(logger, shutdownTimeout)
	if register != nil {
		if err := register(ctx, s); err != nil {
			return fmt.Errorf("register: %w", err)
		}
	}
	httpListener, err := net.Listen("tcp", Env("HTTP_ADDR", ":8080"))
	if err != nil {
		return fmt.Errorf("listen HTTP: %w", err)
	}
	grpcListener, err := net.Listen("tcp", Env("GRPC_ADDR", ":9090"))
	if err != nil {
		httpListener.Close()
		return fmt.Errorf("listen gRPC: %w", err)
	}
	// Serve takes ownership of both listeners.
	return s.Serve(ctx, httpListener, grpcListener)
}

func healthcheck() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	httpAddr, err := probeAddress(Env("HTTP_ADDR", ":8080"))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+httpAddr+"/readyz", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP readiness status: %d", resp.StatusCode)
	}
	grpcAddr, err := probeAddress(Env("GRPC_ADDR", ":9090"))
	if err != nil {
		return err
	}
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	status, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		return err
	}
	if status.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		return fmt.Errorf("gRPC readiness status: %s", status.GetStatus())
	}
	return nil
}

func probeAddress(address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", err
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	} else if host == "::" {
		host = "::1"
	}
	return net.JoinHostPort(host, port), nil
}

// Env returns the environment variable key, or fallback when it is unset or empty.
func Env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
