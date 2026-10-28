// Package service implements the iam contract on top of domain and store.
package service

import "github.com/Ceferinto/habbyte/backend/internal/modules/iam/contract"

// Service implements contract.Service.
type Service struct{}

var _ contract.Service = (*Service)(nil)

// New returns the iam service.
func New() *Service { return &Service{} }
