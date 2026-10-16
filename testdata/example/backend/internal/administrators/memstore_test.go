package administrators

import (
	"context"
	"slices"
	"strings"
	"sync"
)

// memoryStore is an in-memory store for tests that do not need Postgres.
type memoryStore struct {
	mu             sync.RWMutex
	administrators map[string]administrator // by ID
}

func newMemoryStore() *memoryStore {
	return &memoryStore{administrators: make(map[string]administrator)}
}

func (m *memoryStore) create(_ context.Context, a administrator) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.administrators {
		if existing.TenantID != a.TenantID {
			continue
		}
		if strings.EqualFold(existing.Email, a.Email) {
			return errEmailInUse
		}
		if a.KeycloakSub != "" && existing.KeycloakSub == a.KeycloakSub {
			return errKeycloakSubInUse
		}
	}
	m.administrators[a.ID] = a
	return nil
}

func (m *memoryStore) get(_ context.Context, tenantID, id string) (administrator, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.administrators[id]
	if !ok || a.TenantID != tenantID {
		return administrator{}, errNotFound
	}
	return a, nil
}

func (m *memoryStore) list(_ context.Context, q listQuery) ([]administrator, error) {
	m.mu.RLock()
	var matches []administrator
	for _, a := range m.administrators {
		if a.TenantID == q.TenantID && (q.After == nil || compare(a, *q.After) > 0) {
			matches = append(matches, a)
		}
	}
	m.mu.RUnlock()
	slices.SortFunc(matches, func(a, b administrator) int {
		return compare(a, cursor{CreateTime: b.CreateTime, ID: b.ID})
	})
	return matches[:min(q.Limit, len(matches))], nil
}

func compare(a administrator, c cursor) int {
	if n := a.CreateTime.Compare(c.CreateTime); n != 0 {
		return n
	}
	return strings.Compare(a.ID, c.ID)
}
