package postgres

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"testing/fstest"
)

// TestOpenAndMigrate runs against TEST_DATABASE_URL and is skipped without it.
func TestOpenAndMigrate(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool, err := Open(ctx, logger, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close) // runs after cleanup below
	cleanup := func() {
		_, _ = pool.Exec(ctx, "DROP SCHEMA IF EXISTS platform_test CASCADE")
		_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS goose_db_version")
	}
	cleanup()
	t.Cleanup(cleanup)
	migrations := fstest.MapFS{
		"00001_create.sql": {Data: []byte(`-- +goose Up
CREATE SCHEMA platform_test;
CREATE TABLE platform_test.probe (id int PRIMARY KEY);
-- +goose Down
DROP SCHEMA platform_test CASCADE;
`)},
	}
	// Running twice proves pending-only behavior.
	for range 2 {
		if err := Migrate(ctx, logger, pool, migrations); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, "INSERT INTO platform_test.probe VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, "INSERT INTO platform_test.probe VALUES (1)")
	if !IsUniqueViolation(err, "") {
		t.Fatalf("duplicate insert error = %v, want a unique violation", err)
	}
}
