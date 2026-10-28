package residents

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/CMOT/habyta/backend/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// migrationFiles holds the goose SQL files at its root, as postgres.Migrate expects.
var migrationFiles = func() fs.FS {
	sub, err := fs.Sub(embeddedMigrations, "migrations")
	if err != nil {
		panic(err)
	}
	return sub
}()

type postgresStore struct {
	pool *pgxpool.Pool
}

const residentColumns = `id, tenant_id, unit_id, full_name, email, phone,
	coalesce(keycloak_sub::text, ''), create_time, update_time`

func (p *postgresStore) create(ctx context.Context, r resident) error {
	var keycloakSub *string
	if r.KeycloakSub != "" {
		keycloakSub = &r.KeycloakSub
	}
	_, err := p.pool.Exec(ctx, `
		INSERT INTO residents (id, tenant_id, unit_id, full_name, email, phone, keycloak_sub, create_time, update_time)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		r.ID, r.TenantID, r.UnitID, r.FullName, r.Email, r.Phone, keycloakSub, r.CreateTime, r.UpdateTime)
	switch {
	case postgres.IsUniqueViolation(err, "residents_tenant_email_key"):
		return errEmailInUse
	case postgres.IsUniqueViolation(err, "residents_tenant_keycloak_sub_key"):
		return errKeycloakSubInUse
	case err != nil:
		return fmt.Errorf("insert resident: %w", err)
	}
	return nil
}

func (p *postgresStore) get(ctx context.Context, tenantID, id string) (resident, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+residentColumns+` FROM residents WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	if err != nil {
		return resident{}, fmt.Errorf("get resident: %w", err)
	}
	r, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[resident])
	if errors.Is(err, pgx.ErrNoRows) {
		return resident{}, errNotFound
	}
	if err != nil {
		return resident{}, fmt.Errorf("get resident: %w", err)
	}
	return r, nil
}

func (p *postgresStore) list(ctx context.Context, q listQuery) ([]resident, error) {
	var sql strings.Builder
	sql.WriteString(`SELECT ` + residentColumns + ` FROM residents WHERE tenant_id = $1`)
	args := []any{q.TenantID}
	if q.UnitID != "" {
		args = append(args, q.UnitID)
		fmt.Fprintf(&sql, ` AND unit_id = $%d`, len(args))
	}
	if q.After != nil {
		args = append(args, q.After.CreateTime, q.After.ID)
		fmt.Fprintf(&sql, ` AND (create_time, id) > ($%d, $%d)`, len(args)-1, len(args))
	}
	args = append(args, q.Limit)
	fmt.Fprintf(&sql, ` ORDER BY create_time, id LIMIT $%d`, len(args))
	rows, err := p.pool.Query(ctx, sql.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("list residents: %w", err)
	}
	residents, err := pgx.CollectRows(rows, pgx.RowToStructByPos[resident])
	if err != nil {
		return nil, fmt.Errorf("list residents: %w", err)
	}
	return residents, nil
}
