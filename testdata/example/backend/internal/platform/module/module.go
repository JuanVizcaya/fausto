// Package module defines how a module joins a binary. The MVP binary adds
// every module to one Registry; an extracted service adds only its own.
package module

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
)

// Module is a bounded context: it owns its schema and exposes a contract.
type Module interface {
	// Name is the module's lowercase name, also its schema and package name.
	Name() string
	// Register provides the module's contract implementation to r.
	Register(r *Registry) error
}

var validName = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// Registry holds the modules in a binary and their contract implementations.
// Permissions, event subscriptions, jobs and migrations join it as the
// platform grows.
type Registry struct {
	names     []string
	seen      map[string]bool
	contracts map[reflect.Type]any
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{seen: map[string]bool{}, contracts: map[reflect.Type]any{}}
}

// Add registers m. Names must be unique and valid.
func (r *Registry) Add(m Module) error {
	name := m.Name()
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid module name %q", name)
	}
	if r.seen[name] {
		return fmt.Errorf("module %q registered twice", name)
	}
	r.seen[name] = true
	r.names = append(r.names, name)
	if err := m.Register(r); err != nil {
		return fmt.Errorf("register %s: %w", name, err)
	}
	return nil
}

// Names returns the registered modules in the order they were added.
func (r *Registry) Names() []string { return append([]string(nil), r.names...) }

// Provide records impl as the implementation of the contract interface C.
func Provide[C any](r *Registry, impl C) error {
	t := reflect.TypeFor[C]()
	if t.Kind() != reflect.Interface {
		return fmt.Errorf("contract %s is not an interface", t)
	}
	if any(impl) == nil {
		return fmt.Errorf("nil implementation for %s", t)
	}
	if _, ok := r.contracts[t]; ok {
		return fmt.Errorf("contract %s provided twice", t)
	}
	r.contracts[t] = impl
	return nil
}

// ErrNotProvided means no module in the binary provides the contract.
var ErrNotProvided = errors.New("contract not provided")

// Lookup returns the implementation of the contract interface C.
func Lookup[C any](r *Registry) (C, error) {
	impl, ok := r.contracts[reflect.TypeFor[C]()]
	if !ok {
		var zero C
		return zero, fmt.Errorf("%w: %s", ErrNotProvided, reflect.TypeFor[C]())
	}
	return impl.(C), nil
}
