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
	"maps"
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
	dir      string // the materialized result, the spec included
	// files are what the edits wrote that go into a commit: the spec (when
	// the repository keeps it), and the files generated from the charters.
	files edit.Files
	// For a spec fausto keeps: overlay puts it in the base tree, and spec
	// are the spec files the edits changed (nil removes one); they go into
	// a revision, not into a commit.
	overlay edit.Files
	spec    edit.Files
	cleanup func()
}

// kept reports whether fausto keeps the project's spec.
func (a *applied) kept() bool { return a.overlay != nil }

// specOptions are the options to read the result with.
func (a *applied) specOptions() (spec.Options, error) {
	if !a.kept() {
		return spec.Options{}, nil
	}
	files, err := spec.ReadDir(a.dir)
	return spec.Options{Files: files}, err
}

// apply materializes baseHead, with the spec fausto keeps (kept, nil when
// it keeps none), and changes applied (dated date). A failure to apply is
// returned as a ConflictError.
func (s *Store) apply(baseHead string, changes []edit.Change, date string, kept map[string][]byte) (*applied, error) {
	dir, cleanup, err := s.materialize(baseHead)
	if err != nil {
		return nil, err
	}
	a := &applied{baseHead: baseHead, dir: dir, cleanup: cleanup}
	a.overlay, err = s.overlay(baseHead, kept)
	if err == nil {
		err = writeFiles(dir, a.overlay)
	}
	if err == nil {
		a.files, err = edit.Apply(dir, changes, date)
	}
	if err == nil {
		err = writeFiles(dir, a.files)
	}
	if err == nil && a.kept() {
		a.spec = edit.Files{}
		for path, data := range a.files {
			if isSpecPath(path) {
				a.spec[path] = data
				delete(a.files, path)
			}
		}
	}
	if err == nil {
		err = syncAgentDefs(dir, a.files, a.kept())
	}
	if err != nil {
		cleanup()
		var conflict *ConflictError
		if errors.As(err, &conflict) {
			return nil, err
		}
		return nil, &ConflictError{fmt.Sprintf("The changes don't apply to %s: %v", s.Base(), err)}
	}
	return a, nil
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
	kept, rev, err := s.keptFiles(ctx)
	if err != nil {
		return nil, err
	}
	a, err := s.apply(baseHead, changes, today, kept)
	if err != nil {
		var conflict *ConflictError
		if errors.As(err, &conflict) {
			return nil, errors.New(strings.Replace(conflict.Reason, "The changes don't apply", "The changes can't be applied", 1))
		}
		return nil, err
	}
	defer a.cleanup()
	opts, err := a.specOptions()
	if err != nil {
		return nil, err
	}
	if err := validate(a.dir, opts); err != nil {
		return nil, err
	}
	milestone = strings.TrimSpace(milestone)
	if milestone != "" {
		if sp, err := spec.LoadWith(a.dir, opts); err != nil || sp.Milestone(milestone) == nil {
			return nil, &InvalidError{Findings: []check.Finding{{Message: fmt.Sprintf("proposal targets unknown milestone %q", milestone)}}}
		}
	}

	p := &store.Proposal{
		Title: title, Description: strings.TrimSpace(description), Author: s.author(), Created: today, Status: "open",
		Milestone: milestone, BaseCommit: baseHead, Changes: changes, Tasks: []string{},
	}
	if rev != nil {
		p.BaseRevision = rev.N
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

// syncAgentDefs regenerates the files generated from the charters in the
// materialized tree dir, so a proposal that changes a charter carries them,
// and adds them to files: CODEOWNERS, and the agent definitions unless
// fausto keeps the charters (kept), and serves the definitions itself. A
// spec that doesn't load is left for validate to report.
func syncAgentDefs(dir string, files edit.Files, kept bool) error {
	s, err := spec.Load(dir)
	if err != nil {
		return nil
	}
	defs, err := agentdef.Sync(dir, s)
	if err != nil {
		return err
	}
	if kept {
		maps.DeleteFunc(defs, func(p string, _ []byte) bool { return p != agentdef.CodeOwners })
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
func validate(dir string, o spec.Options) error {
	report, err := check.RunWith(dir, []string{"schema"}, o)
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
		// Spec changes fausto keeps are in no commit: those of the revision
		// it wrote, or, until it is applied, the diff recorded on accept.
		if p.Revision > 1 {
			diff, files, changes, err := s.RevisionDiff(ctx, p.Revision)
			if err != nil {
				return nil, err
			}
			d.Diff = joinDiffs(d.Diff, diff)
			d.Files = append(d.Files, files...)
			d.Changes = append(d.Changes, changes...)
		} else if p.SpecDiff != "" {
			d.Diff = joinDiffs(d.Diff, p.SpecDiff)
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
	kept, rev, err := s.keptFiles(ctx)
	if err != nil {
		return nil, err
	}
	a, err := s.apply(baseHead, p.Changes, p.Created, kept)
	if err != nil {
		var conflict *ConflictError
		if errors.As(err, &conflict) {
			d.Conflict = conflict.Reason
			return d, nil
		}
		return nil, err
	}
	defer a.cleanup()

	// The diff between dangling commits: the base as fausto sees it, and
	// the proposal as accepting it would change it now, charters included.
	// Git collects them later.
	from, to, err := s.preview(a, p.Title)
	if err != nil {
		return nil, err
	}
	if err := s.describeCommit(d, from, to); err != nil {
		return nil, err
	}
	if d.Conflict == "" {
		d.Conflict = s.staleConflict(ctx, p, baseHead, d.Changes, kept, rev)
	}

	opts, err := a.specOptions()
	if err != nil {
		return nil, err
	}
	if d.Report, err = check.RunWith(a.dir, nil, opts); err != nil {
		return nil, err
	}
	merged, _ := spec.LoadWith(a.dir, opts)
	if merged == nil {
		if d.Conflict == "" {
			d.Conflict = "The resulting spec cannot be loaded; see the schema check."
		}
		return d, nil
	}
	if !schemaOK(d.Report) && d.Conflict == "" {
		d.Conflict = "The resulting spec fails the schema check."
	}
	before, err := s.loadSpec(baseHead, kept)
	if err != nil {
		return nil, err
	}
	d.Plans, d.Unassigned = PlanTasks(id, d.Changes, before, merged, d.Report)
	d.Unassigned = nonNil(d.Unassigned)
	return d, nil
}

// preview returns dangling commits from which the base, as fausto sees it,
// becomes the base with a's changes: with fausto's charters in both, when
// it keeps them.
func (s *Store) preview(a *applied, title string) (from, to string, err error) {
	from = a.baseHead
	files := a.files
	if a.kept() {
		if from, err = s.commit(a.baseHead+"^{tree}", a.overlay, []string{a.baseHead}, "Preview: base\n"); err != nil {
			return "", "", err
		}
		files = maps.Clone(a.files)
		maps.Copy(files, a.spec)
	}
	to, err = s.commit(from+"^{tree}", files, []string{from}, "Preview: "+title+"\n")
	return from, to, err
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
	before, err := s.loadSpec(from, nil)
	if err != nil {
		return err
	}
	after, err := s.loadSpec(to, nil)
	if err != nil {
		return err
	}
	if d.Changes = DiffSpecs(before, after); d.Changes == nil {
		d.Changes = []ItemChange{}
	}
	return nil
}

// staleConflict explains why a proposal shouldn't be accepted when an item
// it changes was changed since the proposal was made, on the base branch or
// in the spec fausto keeps: its edits would silently overwrite that change.
func (s *Store) staleConflict(ctx context.Context, p *store.Proposal, baseHead string, changes []ItemChange, kept map[string][]byte, rev *store.SpecRevision) string {
	key := func(c ItemChange) string { return c.Kind + "\x00" + c.ID }
	mine := map[string]bool{}
	for _, c := range changes {
		mine[key(c)] = true
	}
	var clashes []string
	if p.BaseCommit != "" && p.BaseCommit != baseHead {
		// A rewritten base leaves nothing to compare against.
		if _, err := s.revParse(p.BaseCommit); err == nil {
			then, err1 := s.loadSpec(p.BaseCommit, kept)
			now, err2 := s.loadSpec(baseHead, kept)
			if err1 == nil && err2 == nil && then != nil && now != nil {
				for _, c := range DiffSpecs(then, now) {
					if mine[key(c)] {
						clashes = append(clashes, fmt.Sprintf("%s `%s` changed on %s", c.Kind, c.ID, s.Base()))
					}
				}
			}
		}
	}
	if rev != nil && p.BaseRevision > 0 && p.BaseRevision < rev.N {
		then, err1 := s.specAt(ctx, p.BaseRevision)
		now, err2 := s.specAt(ctx, rev.N)
		if err1 == nil && err2 == nil && then != nil && now != nil {
			for _, c := range DiffSpecs(then, now) {
				if mine[key(c)] {
					clashes = append(clashes, fmt.Sprintf("%s `%s` changed in fausto", c.Kind, c.ID))
				}
			}
		}
	}
	if len(clashes) == 0 {
		return ""
	}
	slices.Sort(clashes)
	return strings.Join(clashes, ", ") + " since this proposal was made; review it and propose again."
}

func schemaOK(r *check.Report) bool {
	for _, res := range r.Results {
		if res.Check == "schema" {
			return res.Status == check.Pass
		}
	}
	return true
}

// loadSpec reads the spec at rev, with the spec fausto keeps (kept) in
// place of the tree's own when it keeps one.
func (s *Store) loadSpec(rev string, kept map[string][]byte) (*spec.Spec, error) {
	dir, cleanup, err := s.materialize(rev)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	sp, err := spec.LoadWith(dir, spec.Options{Files: kept})
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
	kept, _, err := s.keptFiles(ctx)
	if err != nil {
		return nil, err
	}
	a, err := s.apply(baseHead, d.Proposal.Changes, d.Created, kept)
	if err != nil {
		return nil, err
	}
	defer a.cleanup()
	opts, err := a.specOptions()
	if err != nil {
		return nil, err
	}
	if err := validate(a.dir, opts); err != nil {
		return nil, err
	}
	tasks := make([]store.Task, 0, len(d.Plans))
	for _, p := range d.Plans {
		tasks = append(tasks, store.Task{
			ID: p.ID, Agent: p.Agent, Proposal: id, Milestone: d.Milestone, Status: "open",
			Title: d.Title, Components: p.Components, Body: taskBody(p, d),
		})
	}
	specDiff := ""
	if len(a.spec) > 0 {
		if specDiff, err = s.specDiff(a, d.Title); err != nil {
			return nil, err
		}
	}
	update := store.ProposalUpdate{Date: s.today(), PendingTasks: tasks, SpecDiff: specDiff}

	if len(a.files) == 0 {
		// Only the spec fausto keeps changes, and nothing in the
		// repository: there is nothing to merge, so it applies now.
		if err := s.DB.MoveProposal(ctx, id, "open", "accepted", update); err != nil {
			return nil, err
		}
		p, err := s.DB.Proposal(ctx, id)
		if err != nil {
			return nil, err
		}
		if _, _, err := s.applyNow(ctx, p, ""); err != nil {
			return nil, err
		}
		return s.DB.Proposal(ctx, id)
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
	update.Commit, update.Branch, update.PR, update.URL = commit, review.Branch, review.PR, review.URL
	if err := s.DB.MoveProposal(ctx, id, "open", "accepted", update); err != nil {
		return nil, fmt.Errorf("the change is on branch %s but marking the proposal accepted failed: %w", review.Branch, err)
	}
	return s.DB.Proposal(ctx, id)
}

// specDiff is the diff of a's changes to the spec fausto keeps, which are
// in no commit.
func (s *Store) specDiff(a *applied, title string) (string, error) {
	from, to, err := s.preview(a, title)
	if err != nil {
		return "", err
	}
	return s.git("", nil, nil, "diff", "--no-color", from, to, "--", spec.Dir+"/")
}

// joinDiffs joins two diffs, either of which may be empty.
func joinDiffs(a, b string) string {
	if a == "" || b == "" {
		return a + b
	}
	return strings.TrimSuffix(a, "\n") + "\n" + b
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
		return s.applyNow(ctx, p, o.Merged)
	case source.Closed:
		return "withdrawn", nil, s.DB.MoveProposal(ctx, p.ID, "accepted", "withdrawn", store.ProposalUpdate{Date: s.today()})
	}
	return "", nil, nil
}

// applyNow applies an accepted proposal whose change is in the base branch
// (merged by commit merged, when known), or that has nothing to merge: it
// applies its changes to the spec fausto keeps, if it keeps one, creates
// its tasks and marks it applied.
func (s *Store) applyNow(ctx context.Context, p *store.Proposal, merged string) (string, []string, error) {
	revision := 0
	if kept, err := s.kept(ctx); err != nil {
		return "", nil, err
	} else if kept != nil {
		if revision, err = s.applyToKept(ctx, p, s.author()); err != nil {
			return "", nil, err
		}
	}
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
	return "applied", ids, s.DB.MoveProposal(ctx, p.ID, "accepted", "applied", store.ProposalUpdate{Date: s.today(), Merged: merged, Tasks: ids, Revision: revision})
}

// contentMerged reports whether every spec item the proposal's commit
// changes is the same in the base branch as in the commit: the change was
// merged some way that doesn't keep the commit, such as a squash.
func (s *Store) contentMerged(p *store.Proposal) bool {
	baseHead, err := s.revParse(s.Src.BaseRef())
	if err != nil || p.Commit == "" {
		return false
	}
	before, err1 := s.loadSpec(p.Commit+"^", nil)
	mine, err2 := s.loadSpec(p.Commit, nil)
	base, err3 := s.loadSpec(baseHead, nil)
	if err1 != nil || err2 != nil || err3 != nil || before == nil || mine == nil || base == nil {
		return false // as for a spec fausto keeps, which is in no commit
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
