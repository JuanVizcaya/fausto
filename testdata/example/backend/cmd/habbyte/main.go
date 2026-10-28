// Command habbyte is the MVP backend: every module in one process behind the
// public API.
package main

import (
	"context"

	"github.com/Ceferinto/habbyte/backend/internal/edge/api"
	"github.com/Ceferinto/habbyte/backend/internal/modules/accesscontrol"
	"github.com/Ceferinto/habbyte/backend/internal/modules/amenities"
	"github.com/Ceferinto/habbyte/backend/internal/modules/audit"
	"github.com/Ceferinto/habbyte/backend/internal/modules/community"
	"github.com/Ceferinto/habbyte/backend/internal/modules/control"
	"github.com/Ceferinto/habbyte/backend/internal/modules/developments"
	"github.com/Ceferinto/habbyte/backend/internal/modules/devices"
	"github.com/Ceferinto/habbyte/backend/internal/modules/files"
	"github.com/Ceferinto/habbyte/backend/internal/modules/finance"
	"github.com/Ceferinto/habbyte/backend/internal/modules/iam"
	"github.com/Ceferinto/habbyte/backend/internal/modules/invoicing"
	"github.com/Ceferinto/habbyte/backend/internal/modules/notifications"
	"github.com/Ceferinto/habbyte/backend/internal/modules/operations"
	"github.com/Ceferinto/habbyte/backend/internal/modules/payments"
	"github.com/Ceferinto/habbyte/backend/internal/platform/app"
	"github.com/Ceferinto/habbyte/backend/internal/platform/httpserver"
	"github.com/Ceferinto/habbyte/backend/internal/platform/module"
)

// modules lists every module in the binary: the core first, then products,
// then shared capabilities.
func modules() []module.Module {
	return []module.Module{
		iam.New(), developments.New(), control.New(),
		finance.New(), payments.New(), invoicing.New(),
		community.New(), amenities.New(), operations.New(),
		accesscontrol.New(), devices.New(),
		notifications.New(), files.New(), audit.New(),
	}
}

func main() {
	app.Main("habbyte", func(_ context.Context, s *httpserver.Server) error {
		registry := module.NewRegistry()
		for _, m := range modules() {
			if err := registry.Add(m); err != nil {
				return err
			}
		}
		s.Logger().Info("modules registered", "modules", registry.Names())
		api.Register(s)
		return nil
	})
}
