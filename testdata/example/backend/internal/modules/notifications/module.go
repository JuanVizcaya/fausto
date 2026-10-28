// Package notifications owns message templates, channels (email, push, in-app
// inbox), preferences, and the delivery log. It reacts to other modules'
// events through a rule table.
package notifications

import (
	"github.com/Ceferinto/habbyte/backend/internal/modules/notifications/contract"
	"github.com/Ceferinto/habbyte/backend/internal/modules/notifications/service"
	"github.com/Ceferinto/habbyte/backend/internal/platform/module"
)

// Name is the module's name and the name of its Postgres schema.
const Name = "notifications"

// Module wires the notifications module into a binary.
type Module struct{}

// New returns the notifications module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return Name }

// Register provides the notifications contract.
func (*Module) Register(r *module.Registry) error {
	return module.Provide[contract.Service](r, service.New())
}
