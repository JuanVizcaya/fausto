// Package service implements the payments contract on top of domain and store.
package service

import "github.com/Ceferinto/habbyte/backend/internal/modules/payments/contract"

// Service implements contract.Service.
type Service struct{}

var _ contract.Service = (*Service)(nil)

// New returns the payments service.
func New() *Service { return &Service{} }
