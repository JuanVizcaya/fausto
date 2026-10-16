// Package memstore is an in-memory store.Store, for tests.
package memstore

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/JuanVizcaya/fausto/internal/store"
)

// Store keeps proposals and tasks in memory. Documents are copied in and
// out, so callers can't change stored state by accident.
type Store struct {
	mu        sync.Mutex
	proposals map[string]store.Proposal
	tasks     map[string]store.Task
	Now       func() time.Time
}

// New returns an empty store.
func New() *Store {
	return &Store{proposals: map[string]store.Proposal{}, tasks: map[string]store.Task{}}
}

var _ store.Store = (*Store)(nil)

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

// clone deep-copies v through JSON.
func clone[T any](v T) T {
	b, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(b, &out)
	return out
}

func cloneProposal(p store.Proposal) store.Proposal {
	out := clone(p)
	out.Changes = clone(p.Changes) // json:"-" on Proposal
	out.PendingTasks = clone(p.PendingTasks)
	return out
}

func (s *Store) CreateProposal(_ context.Context, p *store.Proposal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.proposals[p.ID]; ok {
		return store.ErrExists
	}
	if p.Updated.IsZero() {
		p.Updated = s.now()
	}
	s.proposals[p.ID] = cloneProposal(*p)
	return nil
}

func (s *Store) Proposal(_ context.Context, id string) (*store.Proposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.proposals[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	out := cloneProposal(p)
	return &out, nil
}

func (s *Store) Proposals(context.Context) ([]store.Proposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []store.Proposal{}
	for _, p := range s.proposals {
		out = append(out, cloneProposal(p))
	}
	store.SortProposals(out)
	return out, nil
}

func (s *Store) MoveProposal(_ context.Context, id, from, to string, u store.ProposalUpdate) error {
	if err := store.ValidMove(from, to); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.proposals[id]
	if !ok {
		return store.ErrNotFound
	}
	if p.Status != from {
		return store.ErrChanged
	}
	u.Apply(&p, to)
	p.Updated = s.now()
	s.proposals[id] = cloneProposal(p)
	return nil
}

func (s *Store) CreateTasks(_ context.Context, tasks []store.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range tasks {
		if _, ok := s.tasks[t.ID]; ok {
			return store.ErrExists
		}
	}
	for _, t := range tasks {
		if t.Updated.IsZero() {
			t.Updated = s.now()
		}
		s.tasks[t.ID] = clone(t)
	}
	return nil
}

func (s *Store) Task(_ context.Context, id string) (*store.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	out := clone(t)
	return &out, nil
}

func (s *Store) Tasks(_ context.Context, f store.TaskFilter) ([]store.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []store.Task{}
	for _, t := range s.tasks {
		if f.Match(&t) {
			out = append(out, clone(t))
		}
	}
	store.SortTasks(out)
	return out, nil
}

func (s *Store) SetTaskStatus(_ context.Context, id, from, to, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return store.ErrNotFound
	}
	if t.Status != from {
		return store.ErrChanged
	}
	now := s.now()
	t.History = append(t.History, store.StatusChange{At: now, By: by, From: from, To: to})
	t.Status, t.Updated = to, now
	s.tasks[id] = t
	return nil
}

func (s *Store) SetTaskJira(_ context.Context, id, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return store.ErrNotFound
	}
	t.Jira, t.Updated = key, s.now()
	s.tasks[id] = t
	return nil
}

func (s *Store) Close(context.Context) error { return nil }

// Projects is an in-memory store.Projects.
type Projects struct {
	mu       sync.Mutex
	projects map[string]store.Project
	stores   map[string]*Store
}

// NewProjects returns an empty registry.
func NewProjects() *Projects {
	return &Projects{projects: map[string]store.Project{}, stores: map[string]*Store{}}
}

var _ store.Projects = (*Projects)(nil)

func (r *Projects) Projects(context.Context) ([]store.Project, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []store.Project{}
	for _, p := range r.projects {
		out = append(out, p)
	}
	store.SortProjects(out)
	return out, nil
}

func (r *Projects) Project(_ context.Context, id string) (*store.Project, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.projects[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &p, nil
}

func (r *Projects) PutProject(_ context.Context, p *store.Project) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p.Updated = time.Now().UTC()
	r.projects[p.ID] = *p
	return nil
}

func (r *Projects) DeleteProject(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.projects[id]; !ok {
		return store.ErrNotFound
	}
	delete(r.projects, id)
	return nil
}

func (r *Projects) Store(id string) store.Store {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stores[id] == nil {
		r.stores[id] = New()
	}
	return r.stores[id]
}

func (r *Projects) Close(context.Context) error { return nil }
