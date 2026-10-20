package proposal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JuanVizcaya/fausto/internal/edit"
	"github.com/JuanVizcaya/fausto/internal/spec"
	"github.com/JuanVizcaya/fausto/internal/store"
)

// A project's spec lives either in its repository's system/ or, once
// imported (`fausto spec import`), in fausto's database as numbered
// revisions. For a spec fausto keeps, fausto lays it over system/ wherever
// it reads a spec, so edits, checks and task plans work as for files; a
// proposal's spec changes then go into a new revision when it is applied,
// never into a commit.

// kept returns the latest revision of the spec fausto keeps, or nil when
// the project keeps its own.
func (s *Store) kept(ctx context.Context) (*store.SpecRevision, error) {
	r, err := s.DB.SpecRevision(ctx, 0)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	return r, err
}

// keptFiles is the latest kept revision's files by path, or nil.
func (s *Store) keptFiles(ctx context.Context) (map[string][]byte, *store.SpecRevision, error) {
	r, err := s.kept(ctx)
	if err != nil || r == nil {
		return nil, nil, err
	}
	return r.FileMap(), r, nil
}

// SpecOptions are the options to read the project's spec with: the spec
// fausto keeps, if it keeps one.
func (s *Store) SpecOptions(ctx context.Context) (spec.Options, error) {
	files, _, err := s.keptFiles(ctx)
	return spec.Options{Files: files}, err
}

// overlay returns the files that make the tree at rev hold the spec fausto
// keeps (files) instead of its own system/; nil when fausto keeps none.
func (s *Store) overlay(rev string, files map[string][]byte) (edit.Files, error) {
	if files == nil {
		return nil, nil
	}
	names, err := s.git("", nil, nil, "ls-tree", "-r", "--name-only", rev, "--", spec.Dir+"/")
	if err != nil {
		return nil, err
	}
	out := edit.Files{}
	for _, n := range strings.Fields(names) {
		out[n] = nil
	}
	for p, data := range files {
		out[p] = data
	}
	return out, nil
}

// isSpecPath reports whether path is in the spec.
func isSpecPath(path string) bool { return strings.HasPrefix(path, spec.Dir+"/") }

// specAt reads revision n of the kept spec (with the repository's layout).
func (s *Store) specAt(ctx context.Context, n int) (*spec.Spec, error) {
	r, err := s.DB.SpecRevision(ctx, n)
	if err != nil {
		return nil, err
	}
	sp, err := spec.LoadWith(s.Root(), spec.Options{Files: r.FileMap()})
	if err != nil {
		return nil, nil // reported by the checks; diff against an empty spec
	}
	return sp, nil
}

// applyToKept applies a proposal's changes to the latest revision of the
// kept spec and adds the result as the next revision. It returns the new
// revision's number, or the latest's when the changes change nothing.
// Changes are re-applied rather than copied, so proposals that change
// different items of one file all land.
func (s *Store) applyToKept(ctx context.Context, p *store.Proposal, by string) (int, error) {
	for attempt := 0; ; attempt++ {
		latest, err := s.DB.SpecRevision(ctx, 0)
		if err != nil {
			return 0, err
		}
		dir, err := os.MkdirTemp("", "fausto-spec-")
		if err != nil {
			return 0, err
		}
		files := latest.FileMap()
		if err := writeFiles(dir, files); err != nil {
			os.RemoveAll(dir)
			return 0, err
		}
		changed, err := edit.Apply(dir, p.Changes, p.Created)
		if err == nil {
			err = writeFiles(dir, changed)
		}
		var next map[string][]byte
		if err == nil {
			next, err = spec.ReadDir(dir)
		}
		os.RemoveAll(dir)
		if err != nil {
			return 0, fmt.Errorf("apply its changes to spec revision %d: %w", latest.N, err)
		}
		if sameFiles(files, next) {
			return latest.N, nil
		}
		r := &store.SpecRevision{N: latest.N + 1, Files: store.SpecFiles(next), By: by, Proposal: p.ID, Message: p.Title}
		err = s.DB.AddSpecRevision(ctx, r)
		if errors.Is(err, store.ErrChanged) && attempt < 3 {
			continue // another revision came first; apply on top of it
		}
		return r.N, err
	}
}

func sameFiles(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for p, data := range a {
		if other, ok := b[p]; !ok || string(other) != string(data) {
			return false
		}
	}
	return true
}

// RevisionDiff describes revision n of the kept spec against the one before
// it (against nothing for the first): the diff, the files and the items
// that changed.
func (s *Store) RevisionDiff(ctx context.Context, n int) (diff string, files []string, changes []ItemChange, err error) {
	after, err := s.DB.SpecRevision(ctx, n)
	if err != nil {
		return "", nil, nil, err
	}
	before := &store.SpecRevision{}
	if after.N > 1 {
		if before, err = s.DB.SpecRevision(ctx, after.N-1); err != nil {
			return "", nil, nil, err
		}
	}
	diff, files, err = diffFiles(before.FileMap(), after.FileMap())
	if err != nil {
		return "", nil, nil, err
	}
	load := func(r *store.SpecRevision) *spec.Spec {
		if len(r.Files) == 0 {
			return nil
		}
		sp, _ := spec.LoadWith(s.Root(), spec.Options{Files: r.FileMap()})
		return sp
	}
	return diff, files, nonNilChanges(DiffSpecs(load(before), load(after))), nil
}

// diffFiles is a unified diff between two sets of files by path, as git
// shows it, with the changed paths.
func diffFiles(before, after map[string][]byte) (string, []string, error) {
	root, err := os.MkdirTemp("", "fausto-diff-")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(root)
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	for dir, files := range map[string]map[string][]byte{a: before, b: after} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", nil, err
		}
		if err := writeFiles(dir, files); err != nil {
			return "", nil, err
		}
	}
	run := func(args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-c", "safe.directory=*", "diff", "--no-index", "--no-color"}, args...)...)
		cmd.Dir = root
		out, err := cmd.Output()
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			err = nil // 1 means the trees differ
		}
		return string(out), err
	}
	diff, err := run("--src-prefix=", "--dst-prefix=", "a", "b")
	if err != nil {
		return "", nil, err
	}
	// The trees are a/ and b/, so paths read a/system/x and b/system/x,
	// as in any git diff.
	names, err := run("--name-only", "a", "b")
	if err != nil {
		return "", nil, err
	}
	var files []string
	seen := map[string]bool{}
	for _, n := range strings.Fields(names) {
		n = strings.TrimPrefix(strings.TrimPrefix(n, "a/"), "b/")
		if !seen[n] {
			seen[n] = true
			files = append(files, n)
		}
	}
	return diff, files, nil
}

func nonNilChanges(c []ItemChange) []ItemChange {
	if c == nil {
		return []ItemChange{}
	}
	return c
}

// Restore adds a revision of the kept spec with revision n's files, on
// behalf of by. It returns the new revision.
func (s *Store) Restore(ctx context.Context, n int, by string) (*store.SpecRevision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := s.DB.SpecRevision(ctx, n)
	if err != nil {
		return nil, err
	}
	latest, err := s.DB.SpecRevision(ctx, 0)
	if err != nil {
		return nil, err
	}
	if old.N == latest.N {
		return nil, &ConflictError{fmt.Sprintf("Revision %d is the current spec.", n)}
	}
	if _, err := spec.LoadWith(s.Root(), spec.Options{Files: old.FileMap()}); err != nil {
		return nil, &ConflictError{fmt.Sprintf("Revision %d doesn't load: %v", n, err)}
	}
	r := &store.SpecRevision{N: latest.N + 1, Files: old.Files, By: by, Message: fmt.Sprintf("Restore revision %d", n)}
	if err := s.DB.AddSpecRevision(ctx, r); err != nil {
		return nil, err
	}
	r.Files = nil
	return r, nil
}
