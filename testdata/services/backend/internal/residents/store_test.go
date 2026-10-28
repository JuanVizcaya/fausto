package residents

import (
	"context"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/CMOT/habyta/backend/internal/platform/postgres"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	poolOnce sync.Once
	pool     *pgxpool.Pool
	poolErr  error
)

// newStore returns an empty store: Postgres when TEST_DATABASE_URL is set,
// otherwise the in-memory store. Tests in this package must not run in
// parallel because they share the database.
func newStore(t *testing.T) store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		return newMemoryStore()
	}
	poolOnce.Do(func() {
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		ctx := context.Background()
		if pool, poolErr = postgres.Open(ctx, logger, url); poolErr == nil {
			poolErr = postgres.Migrate(ctx, logger, pool, migrationFiles)
		}
	})
	if poolErr != nil {
		t.Fatal(poolErr)
	}
	if _, err := pool.Exec(context.Background(), "TRUNCATE residents"); err != nil {
		t.Fatal(err)
	}
	return &postgresStore{pool: pool}
}

func TestStoreListOrdersTiesByID(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	same := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	ids := []string{
		"01a0f51b-0000-7000-8000-000000000003",
		"01a0f51b-0000-7000-8000-000000000001",
		"01a0f51b-0000-7000-8000-000000000002",
	}
	for i, id := range ids {
		r := resident{ID: id, TenantID: tenantA, UnitID: unit1, FullName: "R", Email: string(rune('a'+i)) + "@example.com", CreateTime: same, UpdateTime: same}
		if err := s.create(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.list(ctx, listQuery{TenantID: tenantA, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].ID != ids[1] || first[1].ID != ids[2] {
		t.Fatalf("first page = %v", first)
	}
	rest, err := s.list(ctx, listQuery{TenantID: tenantA, Limit: 2, After: &cursor{CreateTime: same, ID: first[1].ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 1 || rest[0].ID != ids[0] {
		t.Fatalf("second page = %v", rest)
	}
}

func TestStoreDuplicateKeycloakSub(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	sub := "9d2f7a3e-1b2c-4d5e-8f90-a1b2c3d4e5f6"
	base := resident{TenantID: tenantA, UnitID: unit1, FullName: "R", KeycloakSub: sub, CreateTime: now, UpdateTime: now}
	first, second := base, base
	first.ID, first.Email = "01a0f51b-0000-7000-8000-000000000001", "a@example.com"
	second.ID, second.Email = "01a0f51b-0000-7000-8000-000000000002", "b@example.com"
	if err := s.create(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := s.create(ctx, second); err != errKeycloakSubInUse {
		t.Fatalf("duplicate keycloak_sub: err = %v, want errKeycloakSubInUse", err)
	}
	// The same Keycloak user may be a resident in another tenant.
	second.TenantID = tenantB
	if err := s.create(ctx, second); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationsEmbeddedAtRoot(t *testing.T) {
	files, err := fs.Glob(migrationFiles, "*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations at the embedded FS root: %v, %v", files, err)
	}
}
