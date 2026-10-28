// Package iam owns accounts linked to Keycloak, invitations, the permission
// catalog, roles and their permissions per development, manual grants, guest
// links, and push device tokens. It answers whether a user may do something in
// a development, reading relationship roles live from developments.
package iam

import (
	"github.com/Ceferinto/habbyte/backend/internal/modules/iam/contract"
	"github.com/Ceferinto/habbyte/backend/internal/modules/iam/service"
	"github.com/Ceferinto/habbyte/backend/internal/platform/module"
)

// Name is the module's name and the name of its Postgres schema.
const Name = "iam"

// Module wires the iam module into a binary.
type Module struct{}

// New returns the iam module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return Name }

// Register provides the iam contract.
func (*Module) Register(r *module.Registry) error {
	return module.Provide[contract.Service](r, service.New())
}
