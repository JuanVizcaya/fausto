// Package service implements the files contract on top of domain and store.
package service

import "github.com/Ceferinto/habbyte/backend/internal/modules/files/contract"

// Service implements contract.Service.
type Service struct{}

var _ contract.Service = (*Service)(nil)

// New returns the files service.
func New() *Service { return &Service{} }
