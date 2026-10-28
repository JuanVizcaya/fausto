// Package community owns announcements, audience segments, documents and
// their access rules, and read receipts.
package community

import (
	"github.com/Ceferinto/habbyte/backend/internal/modules/community/contract"
	"github.com/Ceferinto/habbyte/backend/internal/modules/community/service"
	"github.com/Ceferinto/habbyte/backend/internal/platform/module"
)

// Name is the module's name and the name of its Postgres schema.
const Name = "community"

// Module wires the community module into a binary.
type Module struct{}

// New returns the community module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return Name }

// Register provides the community contract.
func (*Module) Register(r *module.Registry) error {
	return module.Provide[contract.Service](r, service.New())
}
