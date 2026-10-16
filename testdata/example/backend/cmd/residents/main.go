// Command residents serves the residents gRPC API to other services.
package main

import (
	"github.com/CMOT/habyta/backend/internal/platform/service"
	"github.com/CMOT/habyta/backend/internal/residents"
)

func main() {
	service.Main("residents", residents.Register)
}
