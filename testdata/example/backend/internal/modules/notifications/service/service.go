// Package service implements the notifications contract on top of domain and store.
package service

import "github.com/Ceferinto/habbyte/backend/internal/modules/notifications/contract"

// Service implements contract.Service.
type Service struct{}

var _ contract.Service = (*Service)(nil)

// New returns the notifications service.
func New() *Service { return &Service{} }
