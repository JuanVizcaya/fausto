package administrators

import (
	"context"
	"errors"
	"time"
)

var (
	errNotFound         = errors.New("administrator not found")
	errEmailInUse       = errors.New("email already registered in tenant")
	errKeycloakSubInUse = errors.New("keycloak_sub already linked to an administrator in tenant")
	errInvalidPageToken = errors.New("invalid page token")
)

// administrator is the stored form; it is converted to protobuf at the edge.
type administrator struct {
	ID, TenantID, Company, Email, Phone, KeycloakSub string
	CreateTime, UpdateTime                           time.Time
}

// cursor is the position after which a list page starts. Lists are ordered
// by (CreateTime, ID), which is stable and index-friendly.
type cursor struct {
	CreateTime time.Time
	ID         string
}

type listQuery struct {
	TenantID string
	After    *cursor // nil for the first page
	Limit    int
}

// store persists administrators. Implementations must scope every read to
// the tenant and return errNotFound, errEmailInUse, or errKeycloakSubInUse.
type store interface {
	create(ctx context.Context, a administrator) error
	get(ctx context.Context, tenantID, id string) (administrator, error)
	list(ctx context.Context, q listQuery) ([]administrator, error)
}
