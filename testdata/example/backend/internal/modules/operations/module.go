// Package operations owns maintenance tasks, recurring schedules,
// assignments, evidence, impediments and validation.
package operations

import (
	"github.com/Ceferinto/habbyte/backend/internal/modules/operations/contract"
	"github.com/Ceferinto/habbyte/backend/internal/modules/operations/service"
	"github.com/Ceferinto/habbyte/backend/internal/platform/module"
)

// Name is the module's name and the name of its Postgres schema.
const Name = "operations"

// Module wires the operations module into a binary.
type Module struct{}

// New returns the operations module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return Name }

// Register provides the operations contract.
func (*Module) Register(r *module.Registry) error {
	return module.Provide[contract.Service](r, service.New())
}
