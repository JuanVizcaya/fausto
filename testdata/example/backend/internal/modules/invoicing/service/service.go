// Package service implements the invoicing contract on top of domain and store.
package service

import "github.com/Ceferinto/habbyte/backend/internal/modules/invoicing/contract"

// Service implements contract.Service.
type Service struct{}

var _ contract.Service = (*Service)(nil)

// New returns the invoicing service.
func New() *Service { return &Service{} }
