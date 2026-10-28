// Package invoicing owns fiscal profiles, signing certificates (CSD), CFDI 4.0
// documents and payment complements, cancellations, and the PAC adapter.
package invoicing

import (
	"github.com/Ceferinto/habbyte/backend/internal/modules/invoicing/contract"
	"github.com/Ceferinto/habbyte/backend/internal/modules/invoicing/service"
	"github.com/Ceferinto/habbyte/backend/internal/platform/module"
)

// Name is the module's name and the name of its Postgres schema.
const Name = "invoicing"

// Module wires the invoicing module into a binary.
type Module struct{}

// New returns the invoicing module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return Name }

// Register provides the invoicing contract.
func (*Module) Register(r *module.Registry) error {
	return module.Provide[contract.Service](r, service.New())
}
