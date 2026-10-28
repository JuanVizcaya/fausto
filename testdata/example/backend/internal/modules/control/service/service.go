// Package service implements the control contract on top of domain and store.
package service

import "github.com/Ceferinto/habbyte/backend/internal/modules/control/contract"

// Service implements contract.Service.
type Service struct{}

var _ contract.Service = (*Service)(nil)

// New returns the control service.
func New() *Service { return &Service{} }
