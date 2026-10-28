package administrators

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

const administratorColumns = `id, tenant_id, company, email, phone,
	coalesce(keycloak_sub::text, ''), create_time, update_time`

func (p *postgresStore) create(ctx context.Context, a administrator) error {
	var keycloakSub *string
	if a.KeycloakSub != "" {
		keycloakSub = &a.KeycloakSub
	}
	_, err := p.pool.Exec(ctx, `
		INSERT INTO administrators (id, tenant_id, company, email, phone, keycloak_sub, create_time, update_time)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		a.ID, a.TenantID, a.Company, a.Email, a.Phone, keycloakSub, a.CreateTime, a.UpdateTime)
	switch {
	case postgres.IsUniqueViolation(err, "administrators_tenant_email_key"):
		return errEmailInUse
	case postgres.IsUniqueViolation(err, "administrators_tenant_keycloak_sub_key"):
		return errKeycloakSubInUse
	case err != nil:
		return fmt.Errorf("insert administrator: %w", err)
	}
	return nil
}

func (p *postgresStore) get(ctx context.Context, tenantID, id string) (administrator, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+administratorColumns+` FROM administrators WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	if err != nil {
		return administrator{}, fmt.Errorf("get administrator: %w", err)
	}
	a, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[administrator])
	if errors.Is(err, pgx.ErrNoRows) {
		return administrator{}, errNotFound
	}
	if err != nil {
		return administrator{}, fmt.Errorf("get administrator: %w", err)
	}
	return a, nil
}

func (p *postgresStore) list(ctx context.Context, q listQuery) ([]administrator, error) {
	var sql strings.Builder
	sql.WriteString(`SELECT ` + administratorColumns + ` FROM administrators WHERE tenant_id = $1`)
	args := []any{q.TenantID}
	if q.After != nil {
		args = append(args, q.After.CreateTime, q.After.ID)
		fmt.Fprintf(&sql, ` AND (create_time, id) > ($%d, $%d)`, len(args)-1, len(args))
	}
	args = append(args, q.Limit)
	fmt.Fprintf(&sql, ` ORDER BY create_time, id LIMIT $%d`, len(args))
	rows, err := p.pool.Query(ctx, sql.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("list administrators: %w", err)
	}
	administrators, err := pgx.CollectRows(rows, pgx.RowToStructByPos[administrator])
	if err != nil {
		return nil, fmt.Errorf("list administrators: %w", err)
	}
	return administrators, nil
}
