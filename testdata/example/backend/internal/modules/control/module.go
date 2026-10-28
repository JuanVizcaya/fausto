// Package control owns Habbyte's clients, plans, the products enabled per
// development, subscriptions, support requests, global settings, and
// break-glass access requests.
package control

import (
	"github.com/Ceferinto/habbyte/backend/internal/modules/control/contract"
	"github.com/Ceferinto/habbyte/backend/internal/modules/control/service"
	"github.com/Ceferinto/habbyte/backend/internal/platform/module"
)

// Name is the module's name and the name of its Postgres schema.
const Name = "control"

// Module wires the control module into a binary.
type Module struct{}

// New returns the control module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return Name }

// Register provides the control contract.
func (*Module) Register(r *module.Registry) error {
	return module.Provide[contract.Service](r, service.New())
}
