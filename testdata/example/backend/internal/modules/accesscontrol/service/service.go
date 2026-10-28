// Package service implements the accesscontrol contract on top of domain and store.
package service

import "github.com/Ceferinto/habbyte/backend/internal/modules/accesscontrol/contract"

// Service implements contract.Service.
type Service struct{}

var _ contract.Service = (*Service)(nil)

// New returns the accesscontrol service.
func New() *Service { return &Service{} }
