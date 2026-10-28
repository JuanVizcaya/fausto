// Package service implements the developments contract on top of domain and store.
package service

import "github.com/Ceferinto/habbyte/backend/internal/modules/developments/contract"

// Service implements contract.Service.
type Service struct{}

var _ contract.Service = (*Service)(nil)

// New returns the developments service.
func New() *Service { return &Service{} }
