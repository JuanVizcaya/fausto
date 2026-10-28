// Package finance owns the double-entry ledger per development: charge
// concepts and fee schedules, charges, payments, allocations, credits,
// deposits, refunds and chargebacks, checkout snapshots, reconciliation,
// expenses, payment evidence, delinquency, statements and reports.
package finance

import (
	"github.com/Ceferinto/habbyte/backend/internal/modules/finance/contract"
	"github.com/Ceferinto/habbyte/backend/internal/modules/finance/service"
	"github.com/Ceferinto/habbyte/backend/internal/platform/module"
)

// Name is the module's name and the name of its Postgres schema.
const Name = "finance"

// Module wires the finance module into a binary.
type Module struct{}

// New returns the finance module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return Name }

// Register provides the finance contract.
func (*Module) Register(r *module.Registry) error {
	return module.Provide[contract.Service](r, service.New())
}
