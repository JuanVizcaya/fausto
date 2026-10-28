// Package amenities owns amenities, rule sets, blocks, availability,
// reservations and approvals.
package amenities

import (
	"github.com/Ceferinto/habbyte/backend/internal/modules/amenities/contract"
	"github.com/Ceferinto/habbyte/backend/internal/modules/amenities/service"
	"github.com/Ceferinto/habbyte/backend/internal/platform/module"
)

// Name is the module's name and the name of its Postgres schema.
const Name = "amenities"

// Module wires the amenities module into a binary.
type Module struct{}

// New returns the amenities module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return Name }

// Register provides the amenities contract.
func (*Module) Register(r *module.Registry) error {
	return module.Provide[contract.Service](r, service.New())
}
