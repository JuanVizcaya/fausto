// Package service implements the audit contract on top of domain and store.
package service

import "github.com/Ceferinto/habbyte/backend/internal/modules/audit/contract"

// Service implements contract.Service.
type Service struct{}

var _ contract.Service = (*Service)(nil)

// New returns the audit service.
func New() *Service { return &Service{} }
