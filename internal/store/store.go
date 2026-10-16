// Package store keeps fausto's workflow state, proposals and the tasks they
// create, outside git. Git holds only the spec and the code; a proposal
// stores the edits it makes to the spec, and accepting it commits just those.
// Store is implemented by memstore (tests) and mongostore (a shared
// MongoDB).
package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/JuanVizcaya/fausto/internal/edit"
)

var (
	// ErrNotFound means no document has the given id.
	ErrNotFound = errors.New("not found")
	// ErrExists means a document with the id already exists.
	ErrExists = errors.New("already exists")
	// ErrChanged means the document's status changed since it was read.
	ErrChanged = errors.New("changed concurrently; reload and retry")
)

// Statuses a task may have.
var TaskStatuses = []string{"open", "in-progress", "done", "dropped"}

// Statuses a proposal may have.
var ProposalStatuses = []string{"open", "accepted", "rejected"}

// Proposal is a proposed change to the spec: the edits it makes, and what
// became of it.
type Proposal struct {
	ID          string `json:"id" bson:"_id"`
	Title       string `json:"title" bson:"title"`
	Description string `json:"description" bson:"description,omitempty"`
	Author      string `json:"author" bson:"author"`
	Created     string `json:"created" bson:"created"` // YYYY-MM-DD
	Status      string `json:"status" bson:"status"`
	Milestone   string `json:"milestone,omitempty" bson:"milestone,omitempty"`
	Accepted    string `json:"accepted,omitempty" bson:"accepted,omitempty"`
	// BaseCommit is the base branch commit the proposal was made against.
	BaseCommit string `json:"baseCommit,omitempty" bson:"baseCommit,omitempty"`
	// Commit is the commit that applied the proposal, once accepted.
	Commit  string        `json:"commit,omitempty" bson:"commit,omitempty"`
	Changes []edit.Change `json:"-" bson:"changes"`
	Tasks   []string      `json:"tasks" bson:"tasks"`
	// Legacy marks a proposal imported from git, whose edits weren't kept.
	Legacy  bool      `json:"legacy,omitempty" bson:"legacy,omitempty"`
	Updated time.Time `json:"updated" bson:"updated"`
}

// Task is work assigned to an agent or a person.
type Task struct {
	ID         string   `json:"id" bson:"_id"`
	Agent      string   `json:"agent" bson:"agent"`
	Proposal   string   `json:"proposal,omitempty" bson:"proposal,omitempty"`
	Milestone  string   `json:"milestone,omitempty" bson:"milestone,omitempty"`
	Status     string   `json:"status" bson:"status"`
	Created    string   `json:"created" bson:"created"` // YYYY-MM-DD
	Title      string   `json:"title" bson:"title"`
	Components []string `json:"components,omitempty" bson:"components,omitempty"`
	Body       string   `json:"body" bson:"body"` // Markdown
	// Jira is the key of the task's Jira issue, once mirrored.
	Jira    string         `json:"jira,omitempty" bson:"jira,omitempty"`
	History []StatusChange `json:"history,omitempty" bson:"history,omitempty"`
	Updated time.Time      `json:"updated" bson:"updated"`
}

// StatusChange records who moved a task from one status to another.
type StatusChange struct {
	At   time.Time `json:"at" bson:"at"`
	By   string    `json:"by" bson:"by"`
	From string    `json:"from" bson:"from"`
	To   string    `json:"to" bson:"to"`
}

// TaskFilter selects tasks; empty fields match everything.
type TaskFilter struct {
	IDs       []string
	Agent     string
	Milestone string
	Statuses  []string
}

// Match reports whether t passes the filter.
func (f TaskFilter) Match(t *Task) bool {
	return (len(f.IDs) == 0 || slices.Contains(f.IDs, t.ID)) &&
		(f.Agent == "" || t.Agent == f.Agent) &&
		(f.Milestone == "" || t.Milestone == f.Milestone) &&
		(len(f.Statuses) == 0 || slices.Contains(f.Statuses, t.Status))
}

// Store keeps proposals and tasks.
type Store interface {
	// CreateProposal adds p; ErrExists if its id is taken.
	CreateProposal(ctx context.Context, p *Proposal) error
	Proposal(ctx context.Context, id string) (*Proposal, error)
	// Proposals returns all proposals, newest first.
	Proposals(ctx context.Context) ([]Proposal, error)
	// CloseProposal moves an open proposal to status (accepted or
	// rejected), recording its tasks and commit; ErrChanged if it isn't
	// open anymore.
	CloseProposal(ctx context.Context, id, status, date, commit string, tasks []string) error

	// CreateTasks adds tasks; ErrExists (and nothing added) if an id is
	// taken.
	CreateTasks(ctx context.Context, tasks []Task) error
	Task(ctx context.Context, id string) (*Task, error)
	// Tasks returns the matching tasks, oldest first.
	Tasks(ctx context.Context, f TaskFilter) ([]Task, error)
	// SetTaskStatus moves a task from status from to status to;
	// ErrChanged if its status isn't from anymore.
	SetTaskStatus(ctx context.Context, id, from, to, by string) error
	// SetTaskJira records the key of the task's Jira issue.
	SetTaskJira(ctx context.Context, id, key string) error

	Close(ctx context.Context) error
}

// ValidStatus returns an error unless status is one of statuses.
func ValidStatus(status string, statuses []string) error {
	if !slices.Contains(statuses, status) {
		return fmt.Errorf("unknown status %q (want one of %s)", status, strings.Join(statuses, ", "))
	}
	return nil
}

// SortTasks orders tasks oldest first, then by id.
func SortTasks(ts []Task) {
	slices.SortFunc(ts, func(a, b Task) int {
		if c := strings.Compare(a.Created, b.Created); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
}

// SortProposals orders proposals newest first.
func SortProposals(ps []Proposal) {
	slices.SortFunc(ps, func(a, b Proposal) int {
		if c := strings.Compare(b.Created, a.Created); c != 0 {
			return c
		}
		if c := b.Updated.Compare(a.Updated); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
}

// taskFront is a task's front matter, in the order task files had it.
type taskFront struct {
	ID         string   `yaml:"id"`
	Agent      string   `yaml:"agent"`
	Proposal   string   `yaml:"proposal,omitempty"`
	Milestone  string   `yaml:"milestone,omitempty"`
	Status     string   `yaml:"status,omitempty"`
	Created    date     `yaml:"created"`
	Title      string   `yaml:"title"`
	Components []string `yaml:"components,omitempty,flow"`
	Jira       string   `yaml:"jira,omitempty"`
}

// date is a YYYY-MM-DD string written plain, as in hand-written front
// matter, rather than quoted.
type date string

func (d date) MarshalYAML() (any, error) {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!timestamp", Value: string(d)}, nil
}

func (d *date) UnmarshalYAML(n *yaml.Node) error {
	*d = date(n.Value)
	return nil
}

// Markdown renders the task as a Markdown file with YAML front matter, the
// form agents read it in. withStatus includes the status, which a copy
// kept elsewhere (a Jira attachment) leaves out since it would go stale.
func (t *Task) Markdown(withStatus bool) []byte {
	f := taskFront{ID: t.ID, Agent: t.Agent, Proposal: t.Proposal, Milestone: t.Milestone, Created: date(t.Created),
		Title: t.Title, Components: t.Components, Jira: t.Jira}
	if withStatus {
		f.Status = t.Status
	}
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	_ = enc.Encode(f) // plain strings and lists always encode
	enc.Close()
	return []byte("---\n" + b.String() + "---\n\n" + strings.TrimLeft(t.Body, "\n"))
}

// ParseTask reads a task file: YAML front matter, then the Markdown body.
func ParseTask(data []byte) (*Task, error) {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	rest, ok := strings.CutPrefix(s, "---\n")
	if !ok {
		return nil, errors.New("no front matter")
	}
	front, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return nil, errors.New("unterminated front matter")
	}
	var f taskFront
	if err := yaml.Unmarshal([]byte(front), &f); err != nil {
		return nil, err
	}
	if f.ID == "" || f.Agent == "" {
		return nil, errors.New("front matter needs id and agent")
	}
	return &Task{ID: f.ID, Agent: f.Agent, Proposal: f.Proposal, Milestone: f.Milestone, Status: f.Status,
		Created: string(f.Created), Title: f.Title, Components: f.Components, Jira: f.Jira, Body: strings.TrimLeft(body, "\n")}, nil
}

// SetStatus moves task id to status on behalf of by, and returns the task
// as it was before. Setting the status it already has changes nothing.
func SetStatus(ctx context.Context, s Store, id, status, by string) (before *Task, err error) {
	if err := ValidStatus(status, TaskStatuses); err != nil {
		return nil, err
	}
	t, err := s.Task(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.Status == status {
		return t, nil
	}
	if err := s.SetTaskStatus(ctx, id, t.Status, status, by); err != nil {
		return nil, err
	}
	return t, nil
}
