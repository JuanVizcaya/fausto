// Package source is where fausto gets a project's repository from and where
// it puts accepted proposals for review: a local clone (Local), or a GitHub
// repository that fausto keeps its own clone of (GitHub). Fausto never
// commits to, or moves, the base branch: an accepted proposal goes to a
// branch of its own, and to a pull request on GitHub, and is merged by
// people.
package source

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Source is a project's repository.
type Source interface {
	// Dir is the git work tree fausto reads the project from and builds
	// proposal commits in.
	Dir() string
	// Base is the branch proposals target.
	Base() string
	// BaseRef is the ref whose commit is the base branch as fausto last
	// saw it.
	BaseRef() string
	// Fetch brings the base branch, and Dir for a clone fausto keeps, up
	// to date.
	Fetch(ctx context.Context) error
	// Publish puts commit up for review on branch, as a pull request
	// titled title with body where there are pull requests.
	Publish(ctx context.Context, branch, commit, title, body string) (*Review, error)
	// Status says what became of a review.
	Status(ctx context.Context, r Review) (*Outcome, error)
	// Web is the repository's web URL, such as "https://github.com/o/r",
	// or "".
	Web() string
}

// Review is an accepted proposal's change, up for review.
type Review struct {
	Branch string `json:"branch"`
	Commit string `json:"commit"`
	PR     int    `json:"pr,omitempty"`
	URL    string `json:"url,omitempty"`
}

// Review states.
const (
	Open   = "open"   // not merged yet
	Merged = "merged" // in the base branch
	Closed = "closed" // closed, or its branch deleted, without merging
)

// Outcome is the state of a review.
type Outcome struct {
	State string
	// Merged is the base branch commit that merged it, when known.
	Merged string
}

// git runs git in dir with extra environment, returning trimmed stdout.
func git(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "safe.directory=*"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// isAncestor reports whether commit a is in the history of commit b.
func isAncestor(ctx context.Context, dir, a, b string) bool {
	_, err := git(ctx, dir, nil, "merge-base", "--is-ancestor", a, b)
	return err == nil
}
