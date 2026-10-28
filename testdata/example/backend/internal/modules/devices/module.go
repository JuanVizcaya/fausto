// Package devices owns the device registry, vendor accounts and adapters, the
// command queue with its delivery states, vendor webhooks, and device health.
package devices

import (
	"github.com/Ceferinto/habbyte/backend/internal/modules/devices/contract"
	"github.com/Ceferinto/habbyte/backend/internal/modules/devices/service"
	"github.com/Ceferinto/habbyte/backend/internal/platform/module"
)

// Name is the module's name and the name of its Postgres schema.
const Name = "devices"

// Module wires the devices module into a binary.
type Module struct{}

// New returns the devices module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return Name }

// Register provides the devices contract.
func (*Module) Register(r *module.Registry) error {
	return module.Provide[contract.Service](r, service.New())
}
