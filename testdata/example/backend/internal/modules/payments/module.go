// Package payments owns provider accounts per development, checkout intents,
// provider payments, webhooks, refunds and idempotency. Mercado Pago is the
// first provider.
package payments

import (
	"github.com/Ceferinto/habbyte/backend/internal/modules/payments/contract"
	"github.com/Ceferinto/habbyte/backend/internal/modules/payments/service"
	"github.com/Ceferinto/habbyte/backend/internal/platform/module"
)

// Name is the module's name and the name of its Postgres schema.
const Name = "payments"

// Module wires the payments module into a binary.
type Module struct{}

// New returns the payments module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return Name }

// Register provides the payments contract.
func (*Module) Register(r *module.Registry) error {
	return module.Provide[contract.Service](r, service.New())
}
