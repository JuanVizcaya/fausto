package administrators

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

// testDatabaseEnv names the administrators test database. It is separate
// from TEST_DATABASE_URL, which points at the residents test database: each
// service owns its database, in tests too.
const testDatabaseEnv = "ADMINISTRATORS_TEST_DATABASE_URL"

var (
	poolOnce sync.Once
	pool     *pgxpool.Pool
	poolErr  error
)

// newStore returns an empty store: Postgres when ADMINISTRATORS_TEST_DATABASE_URL
// is set, otherwise the in-memory store. Tests in this package must not run
// in parallel because they share the database.
func newStore(t *testing.T) store {
	t.Helper()
	url := os.Getenv(testDatabaseEnv)
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
	if _, err := pool.Exec(context.Background(), "TRUNCATE administrators"); err != nil {
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
		a := administrator{ID: id, TenantID: tenantA, Company: "A", Email: string(rune('a'+i)) + "@example.com", CreateTime: same, UpdateTime: same}
		if err := s.create(ctx, a); err != nil {
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
	base := administrator{TenantID: tenantA, Company: "A", KeycloakSub: sub, CreateTime: now, UpdateTime: now}
	first, second := base, base
	first.ID, first.Email = "01a0f51b-0000-7000-8000-000000000001", "a@example.com"
	second.ID, second.Email = "01a0f51b-0000-7000-8000-000000000002", "b@example.com"
	if err := s.create(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := s.create(ctx, second); err != errKeycloakSubInUse {
		t.Fatalf("duplicate keycloak_sub: err = %v, want errKeycloakSubInUse", err)
	}
	// The same Keycloak user may administer another tenant.
	second.TenantID = tenantB
	if err := s.create(ctx, second); err != nil {
		t.Fatal(err)
	}
}

func TestStoreDuplicateEmail(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	a := administrator{ID: "01a0f51b-0000-7000-8000-000000000001", TenantID: tenantA, Company: "A", Email: "a@example.com", CreateTime: now, UpdateTime: now}
	if err := s.create(ctx, a); err != nil {
		t.Fatal(err)
	}
	a.ID = "01a0f51b-0000-7000-8000-000000000002"
	if err := s.create(ctx, a); err != errEmailInUse {
		t.Fatalf("duplicate email: err = %v, want errEmailInUse", err)
	}
}

func TestMigrationsEmbeddedAtRoot(t *testing.T) {
	files, err := fs.Glob(migrationFiles, "*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations at the embedded FS root: %v, %v", files, err)
	}
}
