// Command administrators serves the administrators gRPC API to other services.
package main

import (
	"github.com/CMOT/habyta/backend/internal/administrators"
	"github.com/CMOT/habyta/backend/internal/platform/service"
)

func main() {
	service.Main("administrators", administrators.Register)
}
