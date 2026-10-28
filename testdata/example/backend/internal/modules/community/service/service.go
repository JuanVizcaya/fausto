// Package service implements the community contract on top of domain and store.
package service

import "github.com/Ceferinto/habbyte/backend/internal/modules/community/contract"

// Service implements contract.Service.
type Service struct{}

var _ contract.Service = (*Service)(nil)

// New returns the community service.
func New() *Service { return &Service{} }
