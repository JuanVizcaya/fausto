package residents

import (
	"context"
	"slices"
	"strings"
	"sync"
)

// memoryStore is an in-memory store for tests that do not need Postgres.
type memoryStore struct {
	mu        sync.RWMutex
	residents map[string]resident // by ID
}

func newMemoryStore() *memoryStore {
	return &memoryStore{residents: make(map[string]resident)}
}

func (m *memoryStore) create(_ context.Context, r resident) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.residents {
		if existing.TenantID != r.TenantID {
			continue
		}
		if strings.EqualFold(existing.Email, r.Email) {
			return errEmailInUse
		}
		if r.KeycloakSub != "" && existing.KeycloakSub == r.KeycloakSub {
			return errKeycloakSubInUse
		}
	}
	m.residents[r.ID] = r
	return nil
}

func (m *memoryStore) get(_ context.Context, tenantID, id string) (resident, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.residents[id]
	if !ok || r.TenantID != tenantID {
		return resident{}, errNotFound
	}
	return r, nil
}

func (m *memoryStore) list(_ context.Context, q listQuery) ([]resident, error) {
	m.mu.RLock()
	var matches []resident
	for _, r := range m.residents {
		if r.TenantID == q.TenantID && (q.UnitID == "" || r.UnitID == q.UnitID) &&
			(q.After == nil || compare(r, *q.After) > 0) {
			matches = append(matches, r)
		}
	}
	m.mu.RUnlock()
	slices.SortFunc(matches, func(a, b resident) int {
		return compare(a, cursor{CreateTime: b.CreateTime, ID: b.ID})
	})
	return matches[:min(q.Limit, len(matches))], nil
}

func compare(r resident, c cursor) int {
	if n := r.CreateTime.Compare(c.CreateTime); n != 0 {
		return n
	}
	return strings.Compare(r.ID, c.ID)
}
