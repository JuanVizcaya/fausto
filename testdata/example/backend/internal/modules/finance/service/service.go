// Package service implements the finance contract on top of domain and store.
package service

import "github.com/Ceferinto/habbyte/backend/internal/modules/finance/contract"

// Service implements contract.Service.
type Service struct{}

var _ contract.Service = (*Service)(nil)

// New returns the finance service.
func New() *Service { return &Service{} }
