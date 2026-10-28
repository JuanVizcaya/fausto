// Package accesscontrol owns access points, zones, access policies,
// credentials with a lease, the access log, and consent records.
package accesscontrol

import (
	"github.com/Ceferinto/habbyte/backend/internal/modules/accesscontrol/contract"
	"github.com/Ceferinto/habbyte/backend/internal/modules/accesscontrol/service"
	"github.com/Ceferinto/habbyte/backend/internal/platform/module"
)

// Name is the module's name and the name of its Postgres schema.
const Name = "accesscontrol"

// Module wires the accesscontrol module into a binary.
type Module struct{}

// New returns the accesscontrol module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return Name }

// Register provides the accesscontrol contract.
func (*Module) Register(r *module.Registry) error {
	return module.Provide[contract.Service](r, service.New())
}
