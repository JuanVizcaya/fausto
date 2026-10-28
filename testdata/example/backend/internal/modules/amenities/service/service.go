// Package service implements the amenities contract on top of domain and store.
package service

import "github.com/Ceferinto/habbyte/backend/internal/modules/amenities/contract"

// Service implements contract.Service.
type Service struct{}

var _ contract.Service = (*Service)(nil)

// New returns the amenities service.
func New() *Service { return &Service{} }
