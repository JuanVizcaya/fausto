// Package audit owns the central cross-module audit view, built from each
// module's own audit table.
package audit

import (
	"github.com/Ceferinto/habbyte/backend/internal/modules/audit/contract"
	"github.com/Ceferinto/habbyte/backend/internal/modules/audit/service"
	"github.com/Ceferinto/habbyte/backend/internal/platform/module"
)

// Name is the module's name and the name of its Postgres schema.
const Name = "audit"

// Module wires the audit module into a binary.
type Module struct{}

// New returns the audit module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return Name }

// Register provides the audit contract.
func (*Module) Register(r *module.Registry) error {
	return module.Provide[contract.Service](r, service.New())
}
