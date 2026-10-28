// Package service implements the devices contract on top of domain and store.
package service

import "github.com/Ceferinto/habbyte/backend/internal/modules/devices/contract"

// Service implements contract.Service.
type Service struct{}

var _ contract.Service = (*Service)(nil)

// New returns the devices service.
func New() *Service { return &Service{} }
