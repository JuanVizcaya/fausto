package residents

import (
	"context"
	"errors"
	"time"
)

var (
	errNotFound         = errors.New("resident not found")
	errEmailInUse       = errors.New("email already registered in tenant")
	errKeycloakSubInUse = errors.New("keycloak_sub already linked to a resident in tenant")
	errInvalidPageToken = errors.New("invalid page token")
)

// resident is the stored form; it is converted to protobuf at the edge.
type resident struct {
	ID, TenantID, UnitID, FullName, Email, Phone, KeycloakSub string
	CreateTime, UpdateTime                                    time.Time
}

// cursor is the position after which a list page starts. Lists are ordered
// by (CreateTime, ID), which is stable and index-friendly.
type cursor struct {
	CreateTime time.Time
	ID         string
}

type listQuery struct {
	TenantID string
	UnitID   string  // optional filter
	After    *cursor // nil for the first page
	Limit    int
}

// store persists residents. Implementations must scope every read to the
// tenant and return errNotFound, errEmailInUse, or errKeycloakSubInUse.
type store interface {
	create(ctx context.Context, r resident) error
	get(ctx context.Context, tenantID, id string) (resident, error)
	list(ctx context.Context, q listQuery) ([]resident, error)
}
