// Package postgres opens connection pools and applies schema migrations for
// services that store data in Postgres. Each service owns its own database.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// connectTimeout bounds how long Open waits for the database to accept
// connections, for example while its container is still starting.
const connectTimeout = 30 * time.Second

// Open creates a pool for url and waits until the database answers a ping.
func Open(ctx context.Context, logger *slog.Logger, url string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		// The parse error may include the password; do not wrap it.
		return nil, errors.New("invalid database URL")
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	deadline := time.Now().Add(connectTimeout)
	for delay := 250 * time.Millisecond; ; delay = min(delay*2, 4*time.Second) {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err = pool.Ping(pingCtx)
		cancel()
		if err == nil {
			return pool, nil
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			pool.Close()
			return nil, fmt.Errorf("connect to database: %w", err)
		}
		logger.Info("waiting for database", "error", err, "retry_in", delay.String())
		select {
		case <-ctx.Done():
		case <-time.After(delay):
		}
	}
}

// Migrate applies the pending migrations in migrations (goose SQL files at
// its root). A Postgres advisory lock serializes concurrent replicas.
func Migrate(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool, migrations fs.FS) error {
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("migration lock: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	for _, r := range results {
		logger.Info("applied migration", "source", r.Source.Path, "duration_ms", r.Duration.Milliseconds())
	}
	return nil
}

// IsUniqueViolation reports whether err is a unique constraint violation on
// constraint (any constraint when empty).
func IsUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && (constraint == "" || pgErr.ConstraintName == constraint)
}

// IsUnavailable reports whether err means the database could not be reached
// or the statement was never sent, so the caller may retry later. Services
// map it to codes.Unavailable instead of Internal.
func IsUnavailable(err error) bool {
	var connectErr *pgconn.ConnectError
	return errors.As(err, &connectErr) || pgconn.SafeToRetry(err) || pgconn.Timeout(err)
}
