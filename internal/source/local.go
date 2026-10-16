package source

import (
	"context"
	"errors"
	"strings"
)

// Local is a clone on this machine. Accepting a proposal creates a branch
// in it, and nothing else: the checked-out branch and the work tree are
// left alone. Someone merges the branch, and fausto sees it in the base
// branch.
type Local struct {
	Path   string
	Branch string // the base branch
	// URL is the repository's web URL, for links, or "".
	URL string
}

// NewLocal returns the clone at path with base branch base; empty base
// means the checked-out branch.
func NewLocal(ctx context.Context, path, base string) (*Local, error) {
	if base == "" {
		out, err := git(ctx, path, nil, "symbolic-ref", "--quiet", "--short", "HEAD")
		if err != nil || out == "" {
			return nil, errors.New("no checked-out branch to propose against; check out a branch, or name the base branch")
		}
		base = out
	}
	return &Local{Path: path, Branch: base}, nil
}

func (l *Local) Dir() string                 { return l.Path }
func (l *Local) Base() string                { return l.Branch }
func (l *Local) BaseRef() string             { return "refs/heads/" + l.Branch }
func (l *Local) Fetch(context.Context) error { return nil }
func (l *Local) Web() string                 { return l.URL }

// Publish creates branch at commit. A branch that already points at commit
// is fine (a retried accept); one that points elsewhere is an error.
func (l *Local) Publish(ctx context.Context, branch, commit, _, _ string) (*Review, error) {
	ref := "refs/heads/" + branch
	if cur, err := git(ctx, l.Path, nil, "rev-parse", "--verify", "--quiet", ref); err == nil {
		if cur != commit {
			return nil, errors.New("branch " + branch + " already exists; delete it or reject the proposal")
		}
		return &Review{Branch: branch, Commit: commit}, nil
	}
	if _, err := git(ctx, l.Path, nil, "update-ref", ref, commit, strings.Repeat("0", 40)); err != nil {
		return nil, err
	}
	return &Review{Branch: branch, Commit: commit}, nil
}

// Status is merged once the commit is in the base branch's history (a
// merge or a fast-forward), and closed once the branch is gone without
// that. A squash or rebase merge leaves it open here; the caller compares
// contents for those.
func (l *Local) Status(ctx context.Context, r Review) (*Outcome, error) {
	head, err := git(ctx, l.Path, nil, "rev-parse", "--verify", "--quiet", l.BaseRef())
	if err != nil {
		return nil, err
	}
	if isAncestor(ctx, l.Path, r.Commit, head) {
		return &Outcome{State: Merged}, nil
	}
	if _, err := git(ctx, l.Path, nil, "rev-parse", "--verify", "--quiet", "refs/heads/"+r.Branch); err != nil {
		return &Outcome{State: Closed}, nil
	}
	return &Outcome{State: Open}, nil
}
