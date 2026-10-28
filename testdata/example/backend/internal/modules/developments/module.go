// Package developments owns organizations, developments and their setup state,
// sections, units, occupancy links, the committee, the admin assignment,
// governance motions and votes, and bulk import. It enforces the governance
// invariants.
package developments

import (
	"github.com/Ceferinto/habbyte/backend/internal/modules/developments/contract"
	"github.com/Ceferinto/habbyte/backend/internal/modules/developments/service"
	"github.com/Ceferinto/habbyte/backend/internal/platform/module"
)

// Name is the module's name and the name of its Postgres schema.
const Name = "developments"

// Module wires the developments module into a binary.
type Module struct{}

// New returns the developments module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return Name }

// Register provides the developments contract.
func (*Module) Register(r *module.Registry) error {
	return module.Provide[contract.Service](r, service.New())
}
