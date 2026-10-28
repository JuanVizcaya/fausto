// Package files owns file metadata, the owning module and record, size and
// type limits, and presigned S3 uploads and downloads.
package files

import (
	"github.com/Ceferinto/habbyte/backend/internal/modules/files/contract"
	"github.com/Ceferinto/habbyte/backend/internal/modules/files/service"
	"github.com/Ceferinto/habbyte/backend/internal/platform/module"
)

// Name is the module's name and the name of its Postgres schema.
const Name = "files"

// Module wires the files module into a binary.
type Module struct{}

// New returns the files module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return Name }

// Register provides the files contract.
func (*Module) Register(r *module.Registry) error {
	return module.Provide[contract.Service](r, service.New())
}
