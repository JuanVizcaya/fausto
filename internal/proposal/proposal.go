// Package proposal turns spec edits into reviewable proposals and accepted
// proposals into agent tasks.
//
// A proposal is a document in the store (see package store) holding the
// edits it makes to the spec. Its review is computed by applying those
// edits to the base branch as it is now: the diff, the checks on the
// result, and the tasks accepting it would create. Accepting it puts just
// the spec change, as one commit, on a branch of its own (and in a pull
// request on GitHub; see package source) for people to merge; fausto never
// commits to the base branch. Once the change is in the base branch the
// proposal is applied, and its tasks are created. Commits are built with
// git plumbing, so no work tree or index is touched.
package proposal

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/JuanVizcaya/fausto/internal/agentdef"
	"github.com/JuanVizcaya/fausto/internal/check"
	"github.com/JuanVizcaya/fausto/internal/edit"
	"github.com/JuanVizcaya/fausto/internal/source"
	"github.com/JuanVizcaya/fausto/internal/spec"
	"github.com/JuanVizcaya/fausto/internal/store"
)

// Store manages the proposals of the project in Src, keeping them in DB.
type Store struct {
	Src    source.Source
	Author string // "Name <email>"; empty uses git's configured identity
	DB     store.Store
	Now    func() time.Time

	mu sync.Mutex // serializes reviews and status changes
}

// Root is the work tree fausto reads the project from.
func (s *Store) Root() string { return s.Src.Dir() }

// Base is the branch proposals target.
func (s *Store) Base() string { return s.Src.Base() }

// BranchPrefix starts the name of the branch an accepted proposal's change
// goes to: "fausto-proposal/<proposal id>". (Not "fausto/": a project may
// have a branch named fausto, and git can't have both.)
const BranchPrefix = "fausto-proposal/"

// ErrNotFound means no proposal has the given id.
var ErrNotFound = errors.New("proposal not found")

// InvalidError means the proposed spec does not pass the schema check.
type InvalidError struct {
	Findings []check.Finding
}

func (e *InvalidError) Error() string {
	var msgs []string
	for _, f := range e.Findings {
		msgs = append(msgs, f.Message)
	}
	return "the proposed spec is invalid: " + strings.Join(msgs, "; ")
}

// ConflictError means a proposal cannot be accepted as is.
type ConflictError struct{ Reason string }

func (e *ConflictError) Error() string { return e.Reason }

// Detail is a proposal with its review information.
type Detail struct {
	store.Proposal
	Base       string        `json:"base"`
	Changes    []ItemChange  `json:"changes"`
	Files      []string      `json:"files"`
	Diff       string        `json:"diff"`
	Report     *check.Report `json:"report"`
	Plans      []TaskPlan    `json:"plans"`
	Unassigned []string      `json:"unassigned"`
	// Conflict explains why the proposal cannot be accepted, if it can't.
	Conflict string `json:"conflict,omitempty"`
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Store) today() string { return s.now().Format("2006-01-02") }

// applied is the base branch with a proposal's edits applied.
type applied struct {
	baseHead string
	dir      string     // the materialized result
	files    edit.Files // the files the edits wrote, agent definitions included
	cleanup  func()
}

// apply materializes baseHead with changes applied (dated date). A
// failure to apply is returned as a ConflictError.
func (s *Store) apply(baseHead string, changes []edit.Change, date string) (*applied, error) {
	dir, cleanup, err := s.materialize(baseHead)
	if err != nil {
		return nil, err
	}
	files, err := edit.Apply(dir, changes, date)
	if err == nil {
		err = writeFiles(dir, files)
	}
	if err == nil {
		err = syncAgentDefs(dir, files)
	}
	if err != nil {
		cleanup()
		var conflict *ConflictError
		if errors.As(err, &conflict) {
			return nil, err
		}
		return nil, &ConflictError{fmt.Sprintf("The changes don't apply to %s: %v", s.Base(), err)}
	}
	return &applied{baseHead: baseHead, dir: dir, files: files, cleanup: cleanup}, nil
}

// Create records a proposal of changes against the base branch as it is
// now, after checking they apply and pass the schema check, and returns
// its detail. milestone, if set, is the roadmap milestone the work counts
// toward; the tasks created on accept carry it.
func (s *Store) Create(ctx context.Context, title, description, milestone string, changes []edit.Change) (*Detail, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, errors.New("a proposal needs a title")
	}
	if len(changes) == 0 {
		return nil, errors.New("a proposal needs at least one change")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.Src.Fetch(ctx); err != nil {
		return nil, err
	}
	baseHead, err := s.revParse(s.Src.BaseRef())
	if err != nil {
		return nil, fmt.Errorf("base branch %q: %w", s.Base(), err)
	}
	today := s.today()
	a, err := s.apply(baseHead, changes, today)
	if err != nil {
		var conflict *ConflictError
		if errors.As(err, &conflict) {
			return nil, errors.New(strings.Replace(conflict.Reason, "The changes don't apply", "The changes can't be applied", 1))
		}
		return nil, err
	}
	defer a.cleanup()
	if err := validate(a.dir); err != nil {
		return nil, err
	}
	milestone = strings.TrimSpace(milestone)
	if milestone != "" {
		if sp, err := spec.Load(a.dir); err != nil || sp.Milestone(milestone) == nil {
			return nil, &InvalidError{Findings: []check.Finding{{Message: fmt.Sprintf("proposal targets unknown milestone %q", milestone)}}}
		}
	}

	p := &store.Proposal{
		Title: title, Description: strings.TrimSpace(description), Author: s.author(), Created: today, Status: "open",
		Milestone: milestone, BaseCommit: baseHead, Changes: changes, Tasks: []string{},
	}
	for n := 1; ; n++ {
		p.ID = today + "-" + edit.Slug(title, 40)
		if n > 1 {
			p.ID += fmt.Sprintf("-%d", n)
		}
		err := s.DB.CreateProposal(ctx, p)
		if err == nil {
			break
		}
		if !errors.Is(err, store.ErrExists) {
			return nil, err
		}
	}
	return s.detail(ctx, p.ID)
}

// syncAgentDefs regenerates the agent definitions in the materialized tree
// dir, so a proposal that changes a charter carries the matching
// definition, and adds them to files. A spec that doesn't load is left
// for validate to report.
func syncAgentDefs(dir string, files edit.Files) error {
	s, err := spec.Load(dir)
	if err != nil {
		return nil
	}
	defs, err := agentdef.Sync(dir, s)
	if err != nil {
		return err
	}
	if err := agentdef.Write(dir, defs); err != nil {
		return err
	}
	for path, data := range defs {
		files[path] = data
	}
	return nil
}

// validate runs the schema check on a materialized tree.
func validate(dir string) error {
	report, err := check.Run(dir, []string{"schema"})
	if err != nil {
		return err
	}
	if !report.OK {
		return &InvalidError{Findings: report.Results[0].Findings}
	}
	return nil
}

// List returns all proposals, newest first.
func (s *Store) List(ctx context.Context) ([]store.Proposal, error) {
	return s.DB.Proposals(ctx)
}

// Detail returns a proposal with its diff and spec changes; for an open
// one, also the checks on the result of accepting it now and the tasks
// that would create.
func (s *Store) Detail(ctx context.Context, id string) (*Detail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.detail(ctx, id)
}

func (s *Store) detail(ctx context.Context, id string) (*Detail, error) {
	p, err := s.DB.Proposal(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if p.Tasks == nil {
		p.Tasks = []string{}
	}
	d := &Detail{Proposal: *p, Base: s.Base(), Changes: []ItemChange{}, Files: []string{}, Plans: []TaskPlan{}, Unassigned: []string{}}
	switch p.Status {
	case "accepted", "applied":
		// What was accepted: its commit, and the tasks it creates or created.
		if p.Commit != "" {
			if err := s.describeCommit(d, p.Commit+"^", p.Commit); err != nil {
				return nil, err
			}
		}
		for _, t := range p.PendingTasks {
			d.Plans = append(d.Plans, TaskPlan{ID: t.ID, Agent: t.Agent, Components: nonNil(t.Components),
				SpecChanges: []string{}, Findings: []string{}})
		}
		return d, nil
	case "rejected":
		d.Conflict = "This proposal was rejected."
	case "withdrawn":
		d.Conflict = "This proposal was accepted, but its pull request was closed without merging."
	}
	if p.Legacy {
		return d, nil
	}

	baseHead, err := s.revParse(s.Src.BaseRef())
	if err != nil {
		return nil, err
	}
	a, err := s.apply(baseHead, p.Changes, p.Created)
	if err != nil {
		var conflict *ConflictError
		if errors.As(err, &conflict) {
			d.Conflict = conflict.Reason
			return d, nil
		}
		return nil, err
	}
	defer a.cleanup()

	// The diff of a dangling commit: the proposal as accepting it would
	// commit it now. Git collects it later.
	commit, err := s.commit(baseHead+"^{tree}", a.files, []string{baseHead}, "Preview: "+p.Title+"\n")
	if err != nil {
		return nil, err
	}
	if err := s.describeCommit(d, baseHead, commit); err != nil {
		return nil, err
	}
	if d.Conflict == "" {
		d.Conflict = s.staleConflict(p, baseHead, d.Changes)
	}

	if d.Report, err = check.Run(a.dir, nil); err != nil {
		return nil, err
	}
	merged, _ := spec.Load(a.dir)
	if merged == nil {
		if d.Conflict == "" {
			d.Conflict = "The resulting spec cannot be loaded; see the schema check."
		}
		return d, nil
	}
	if !schemaOK(d.Report) && d.Conflict == "" {
		d.Conflict = "The resulting spec fails the schema check."
	}
	before, err := s.loadSpec(baseHead)
	if err != nil {
		return nil, err
	}
	d.Plans, d.Unassigned = PlanTasks(id, d.Changes, before, merged, d.Report)
	d.Unassigned = nonNil(d.Unassigned)
	return d, nil
}

// describeCommit fills d's diff, files, and spec changes from the change
// between two commits.
func (s *Store) describeCommit(d *Detail, from, to string) error {
	var err error
	if d.Diff, err = s.git("", nil, nil, "diff", "--no-color", from, to); err != nil {
		return err
	}
	const maxDiff = 200_000
	if len(d.Diff) > maxDiff {
		d.Diff = d.Diff[:maxDiff] + "\n… diff truncated …"
	}
	names, err := s.git("", nil, nil, "diff", "--name-only", from, to)
	if err != nil {
		return err
	}
	d.Files = nonNil(strings.Fields(names))
	before, err := s.loadSpec(from)
	if err != nil {
		return err
	}
	after, err := s.loadSpec(to)
	if err != nil {
		return err
	}
	if d.Changes = DiffSpecs(before, after); d.Changes == nil {
		d.Changes = []ItemChange{}
	}
	return nil
}

// staleConflict explains why a proposal shouldn't be accepted when an item
// it changes was changed on the base branch since the proposal was made:
// its edits would silently overwrite that change.
func (s *Store) staleConflict(p *store.Proposal, baseHead string, changes []ItemChange) string {
	if p.BaseCommit == "" || p.BaseCommit == baseHead {
		return ""
	}
	if _, err := s.revParse(p.BaseCommit); err != nil {
		return "" // the base was rewritten; nothing to compare against
	}
	then, err := s.loadSpec(p.BaseCommit)
	if err != nil || then == nil {
		return ""
	}
	now, err := s.loadSpec(baseHead)
	if err != nil || now == nil {
		return ""
	}
	key := func(c ItemChange) string { return c.Kind + "\x00" + c.ID }
	mine := map[string]bool{}
	for _, c := range changes {
		mine[key(c)] = true
	}
	var clashes []string
	for _, c := range DiffSpecs(then, now) {
		if mine[key(c)] {
			clashes = append(clashes, fmt.Sprintf("%s `%s`", c.Kind, c.ID))
		}
	}
	if len(clashes) == 0 {
		return ""
	}
	slices.Sort(clashes)
	return fmt.Sprintf("%s changed on %s since this proposal was made; review it and propose again.", strings.Join(clashes, ", "), s.Base())
}

func schemaOK(r *check.Report) bool {
	for _, res := range r.Results {
		if res.Check == "schema" {
			return res.Status == check.Pass
		}
	}
	return true
}

func (s *Store) loadSpec(rev string) (*spec.Spec, error) {
	dir, cleanup, err := s.materialize(rev)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	sp, err := spec.Load(dir)
	if err != nil {
		return nil, nil // reported by the checks; diff against an empty spec
	}
	return sp, nil
}

// Approve accepts an open proposal: it commits its spec change on top of
// the base branch, puts the commit up for review on its own branch (and a
// pull request, on GitHub), and plans a task for each affected agent. The
// tasks are created when the change is merged; see Sync.
func (s *Store) Approve(ctx context.Context, id string) (*store.Proposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.Src.Fetch(ctx); err != nil {
		return nil, err
	}
	d, err := s.detail(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.Status != "open" {
		return nil, &ConflictError{"Only open proposals can be accepted."}
	}
	if d.Legacy {
		return nil, &ConflictError{"This proposal was imported without its changes and can't be accepted."}
	}
	if d.Conflict != "" {
		return nil, &ConflictError{d.Conflict}
	}
	baseHead, err := s.revParse(s.Src.BaseRef())
	if err != nil {
		return nil, err
	}
	a, err := s.apply(baseHead, d.Proposal.Changes, d.Created)
	if err != nil {
		return nil, err
	}
	defer a.cleanup()
	if err := validate(a.dir); err != nil {
		return nil, err
	}

	tasks := make([]store.Task, 0, len(d.Plans))
	for _, p := range d.Plans {
		tasks = append(tasks, store.Task{
			ID: p.ID, Agent: p.Agent, Proposal: id, Milestone: d.Milestone, Status: "open",
			Title: d.Title, Components: p.Components, Body: taskBody(p, d),
		})
	}
	msg := fmt.Sprintf("%s\n\n%s\n\nProposal: %s\n", d.Title, d.Description, id)
	commit, err := s.commit(baseHead+"^{tree}", a.files, []string{baseHead}, msg)
	if err != nil {
		return nil, err
	}
	review, err := s.Src.Publish(ctx, BranchPrefix+id, commit, d.Title, reviewBody(d, tasks))
	if err != nil {
		return nil, err
	}
	err = s.DB.MoveProposal(ctx, id, "open", "accepted", store.ProposalUpdate{
		Date: s.today(), Commit: commit, Branch: review.Branch, PR: review.PR, URL: review.URL, PendingTasks: tasks,
	})
	if err != nil {
		return nil, fmt.Errorf("the change is on branch %s but marking the proposal accepted failed: %w", review.Branch, err)
	}
	return s.DB.Proposal(ctx, id)
}

// reviewBody describes an accepted proposal in its pull request.
func reviewBody(d *Detail, tasks []store.Task) string {
	var b strings.Builder
	if d.Description != "" {
		b.WriteString(d.Description + "\n\n")
	}
	fmt.Fprintf(&b, "Proposal `%s`, accepted in fausto.", d.ID)
	if d.Milestone != "" {
		fmt.Fprintf(&b, " It counts toward milestone `%s`.", d.Milestone)
	}
	b.WriteString("\n\n## Spec changes\n\n")
	for _, c := range d.Changes {
		b.WriteString("- " + c.String() + "\n")
	}
	if len(tasks) > 0 {
		b.WriteString("\n## Tasks, created when this is merged\n\n")
		for _, t := range tasks {
			fmt.Fprintf(&b, "- `%s` for agent `%s`\n", t.ID, t.Agent)
		}
	}
	return b.String()
}

// SyncResult is what Sync did to one proposal.
type SyncResult struct {
	Proposal string   `json:"proposal"`
	Status   string   `json:"status,omitempty"` // the new status, if it changed
	Tasks    []string `json:"tasks,omitempty"`  // the tasks applying it created
	Error    string   `json:"error,omitempty"`
}

// Sync brings accepted proposals up to date with their reviews: one whose
// change is in the base branch becomes applied, and its tasks are created;
// one whose pull request was closed without merging becomes withdrawn.
func (s *Store) Sync(ctx context.Context) ([]SyncResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.DB.Proposals(ctx)
	if err != nil {
		return nil, err
	}
	var accepted []store.Proposal
	for _, p := range all {
		if p.Status == "accepted" {
			accepted = append(accepted, p)
		}
	}
	if len(accepted) == 0 {
		return nil, nil
	}
	if err := s.Src.Fetch(ctx); err != nil {
		return nil, err
	}
	var out []SyncResult
	for _, p := range accepted {
		status, tasks, err := s.sync(ctx, &p)
		r := SyncResult{Proposal: p.ID, Status: status, Tasks: tasks}
		if err != nil {
			r.Error = err.Error()
		}
		if status != "" || err != nil {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *Store) sync(ctx context.Context, p *store.Proposal) (string, []string, error) {
	if p.Branch == "" {
		// Accepted before proposals went through review: its change was
		// committed to the base branch, and its tasks created, right away.
		return "applied", nil, s.DB.MoveProposal(ctx, p.ID, "accepted", "applied", store.ProposalUpdate{Date: p.Accepted})
	}
	o, err := s.Src.Status(ctx, source.Review{Branch: p.Branch, Commit: p.Commit, PR: p.PR, URL: p.URL})
	if err != nil {
		return "", nil, err
	}
	if o.State != source.Merged && s.contentMerged(p) {
		// A squash or rebase merge of a local branch, which is then
		// usually deleted.
		o.State = source.Merged
	}
	switch o.State {
	case source.Merged:
		ids := make([]string, 0, len(p.PendingTasks))
		tasks := make([]store.Task, 0, len(p.PendingTasks))
		for _, t := range p.PendingTasks {
			t.Created = s.today()
			tasks = append(tasks, t)
			ids = append(ids, t.ID)
		}
		// A sync that failed after creating the tasks left them; keep those.
		if err := s.DB.CreateTasks(ctx, tasks); err != nil && !errors.Is(err, store.ErrExists) {
			return "", nil, fmt.Errorf("create its tasks: %w", err)
		}
		return "applied", ids, s.DB.MoveProposal(ctx, p.ID, "accepted", "applied", store.ProposalUpdate{Date: s.today(), Merged: o.Merged, Tasks: ids})
	case source.Closed:
		return "withdrawn", nil, s.DB.MoveProposal(ctx, p.ID, "accepted", "withdrawn", store.ProposalUpdate{Date: s.today()})
	}
	return "", nil, nil
}

// contentMerged reports whether every spec item the proposal's commit
// changes is the same in the base branch as in the commit: the change was
// merged some way that doesn't keep the commit, such as a squash.
func (s *Store) contentMerged(p *store.Proposal) bool {
	baseHead, err := s.revParse(s.Src.BaseRef())
	if err != nil || p.Commit == "" {
		return false
	}
	before, err1 := s.loadSpec(p.Commit + "^")
	mine, err2 := s.loadSpec(p.Commit)
	base, err3 := s.loadSpec(baseHead)
	if err1 != nil || err2 != nil || err3 != nil || before == nil || mine == nil || base == nil {
		return false
	}
	changed := DiffSpecs(before, mine)
	if len(changed) == 0 {
		return false
	}
	key := func(c ItemChange) string { return c.Kind + "\x00" + c.ID }
	differs := map[string]bool{}
	for _, c := range DiffSpecs(mine, base) {
		differs[key(c)] = true
	}
	for _, c := range changed {
		if differs[key(c)] {
			return false
		}
	}
	return true
}

// Reject closes an open proposal without applying it.
func (s *Store) Reject(ctx context.Context, id string) (*store.Proposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.DB.MoveProposal(ctx, id, "open", "rejected", store.ProposalUpdate{Date: s.today()})
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, ErrNotFound
	case errors.Is(err, store.ErrChanged):
		return nil, &ConflictError{"Only open proposals can be rejected."}
	case err != nil:
		return nil, err
	}
	return s.DB.Proposal(ctx, id)
}

// taskBody is the Markdown an agent reads: why the task exists and what
// to change.
func taskBody(p TaskPlan, d *Detail) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Proposal **%s** (`%s`) was accepted and merged.", d.Title, d.ID)
	if d.Milestone != "" {
		fmt.Fprintf(&b, " It counts toward milestone `%s`.", d.Milestone)
	}
	b.WriteString("\n\n")
	if d.Description != "" {
		b.WriteString(d.Description + "\n\n")
	}
	b.WriteString("## Spec changes for you\n\n")
	if len(p.SpecChanges) == 0 {
		b.WriteString("None; this task is about failing checks.\n")
	}
	for _, c := range p.SpecChanges {
		b.WriteString("- " + c + "\n")
	}
	b.WriteString("\n## Checks to fix\n\n")
	if len(p.Findings) == 0 {
		b.WriteString("None at approval time. Keep `fausto check` passing.\n")
	}
	for _, f := range p.Findings {
		b.WriteString("- " + f + "\n")
	}
	fmt.Fprintf(&b, "\n## Done when\n\nThe code matches `system/` for your components and `fausto check` passes.\n"+
		"Read your charter (`system/agents/%s.yaml`) and `system/principles.yaml` first.\n"+
		"Run `fausto task start %s` when you start and `fausto task done %s` when you finish.\n", p.Agent, p.ID, p.ID)
	return b.String()
}
