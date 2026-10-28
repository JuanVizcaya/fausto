// Command api is the HTTP entry point for the React frontend.
package main

import (
	"context"
	"fmt"

	residentsv1 "github.com/CMOT/habyta/backend/gen/habbyte/residents/v1"
	"github.com/CMOT/habyta/backend/internal/api"
	"github.com/CMOT/habyta/backend/internal/platform/grpcclient"
	"github.com/CMOT/habyta/backend/internal/platform/server"
	"github.com/CMOT/habyta/backend/internal/platform/service"
)

func main() {
	service.Main("api", func(_ context.Context, s *server.Server) error {
		residents, err := grpcclient.New(service.Env("RESIDENTS_ADDR", "dns:///residents:9090"))
		if err != nil {
			return fmt.Errorf("residents client: %w", err)
		}
		s.OnShutdown(func() { residents.Close() })
		api.Register(s, s.Logger(), residentsv1.NewResidentServiceClient(residents))
		return nil
	})
}
