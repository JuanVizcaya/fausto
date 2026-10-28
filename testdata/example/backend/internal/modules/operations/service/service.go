// Package service implements the operations contract on top of domain and store.
package service

import "github.com/Ceferinto/habbyte/backend/internal/modules/operations/contract"

// Service implements contract.Service.
type Service struct{}

var _ contract.Service = (*Service)(nil)

// New returns the operations service.
func New() *Service { return &Service{} }
