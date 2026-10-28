// Package prcheck checks a project's open pull requests against the spec
// fausto keeps for it, and reports on GitHub: a commit status, and one
// comment per pull request that it keeps up to date. It is what a
// project's CI can't do once its spec lives in fausto's database.
package prcheck

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JuanVizcaya/fausto/internal/check"
	"github.com/JuanVizcaya/fausto/internal/github"
	"github.com/JuanVizcaya/fausto/internal/spec"
	"github.com/JuanVizcaya/fausto/internal/store"
)

// Context names fausto's commit status.
const Context = "fausto/check"

// marker starts fausto's comment on a pull request, to find it again.
const marker = "<!-- fausto-check -->"

// Checker checks the open pull requests of Repo.
type Checker struct {
	Repo string // "owner/repo"
	GH   *github.Client
	DB   store.Store
	// Clone is fausto's own bare clone for fetching pull requests; the
	// project's clone is never touched.
	Clone string
	// Remote overrides https://github.com/<Repo>.git, for tests.
	Remote string
	// HandOff also publishes the spec revision each pull request is checked
	// against and dispatches EventType (see handOff), before reporting.
	HandOff bool
}

// Result is what checking one pull request did.
type Result struct {
	PR       int    `json:"pr"`
	SHA      string `json:"sha"`
	State    string `json:"state,omitempty"` // success or failure, once reported
	Skipped  bool   `json:"skipped,omitempty"`
	Error    string `json:"error,omitempty"`
	Revision int    `json:"revision"`
}

type pull struct {
	Number int `json:"number"`
	Head   struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

type status struct {
	Context     string `json:"context"`
	Description string `json:"description"`
}

type comment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
}

// Run checks every open pull request not yet checked at the latest spec
// revision. A project whose spec fausto doesn't keep has nothing to check.
func (c *Checker) Run(ctx context.Context) ([]Result, error) {
	rev, err := c.DB.SpecRevision(ctx, 0)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var pulls []pull
	if err := c.GH.Call(ctx, "GET", "/repos/"+c.Repo+"/pulls?state=open&per_page=100", nil, &pulls); err != nil {
		return nil, fmt.Errorf("list pull requests: %w", err)
	}
	var out []Result
	for _, p := range pulls {
		r := Result{PR: p.Number, SHA: p.Head.SHA, Revision: rev.N}
		done, err := c.checked(ctx, p.Head.SHA, rev.N)
		switch {
		case err != nil:
			r.Error = err.Error()
		case done:
			r.Skipped = true
		default:
			r.State, err = c.check(ctx, p, rev)
			if err != nil {
				r.Error = err.Error()
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// revisionTag ends fausto's status description, saying which spec revision
// the commit was checked against.
func revisionTag(n int) string { return fmt.Sprintf(" · spec r%d", n) }

// checked reports whether sha already has fausto's status for revision n.
func (c *Checker) checked(ctx context.Context, sha string, n int) (bool, error) {
	var statuses []status
	if err := c.GH.Call(ctx, "GET", "/repos/"+c.Repo+"/commits/"+sha+"/statuses?per_page=100", nil, &statuses); err != nil {
		return false, err
	}
	for _, s := range statuses { // newest first
		if s.Context == Context {
			return strings.HasSuffix(s.Description, revisionTag(n)), nil
		}
	}
	return false, nil
}

func (c *Checker) check(ctx context.Context, p pull, rev *store.SpecRevision) (string, error) {
	dir, cleanup, err := c.fetch(ctx, p)
	if err != nil {
		return "", err
	}
	defer cleanup()
	report, err := check.RunWith(dir, nil, spec.Options{Files: rev.FileMap()})
	if err != nil {
		return "", err
	}
	failed := 0
	for _, r := range report.Results {
		if r.Status == check.Fail {
			failed++
		}
	}
	state, desc := "success", "The code matches the spec"
	if failed > 0 {
		state, desc = "failure", fmt.Sprintf("%d of %d checks found drift", failed, len(report.Results))
	}
	// Before the status: a pull request with fausto's status counts as
	// checked, so a failed hand-off must leave it to be tried again.
	if c.HandOff {
		if err := c.handOff(ctx, p, rev); err != nil {
			return "", fmt.Errorf("hand off the spec: %w", err)
		}
	}
	if err := c.GH.Call(ctx, "POST", "/repos/"+c.Repo+"/statuses/"+p.Head.SHA, map[string]string{
		"state": state, "context": Context, "description": desc + revisionTag(rev.N),
	}, nil); err != nil {
		return "", fmt.Errorf("post the status: %w", err)
	}
	var b strings.Builder
	b.WriteString(marker + "\n")
	report.WriteMarkdown(&b)
	fmt.Fprintf(&b, "\n<sub>fausto checked %.7s against spec revision %d.</sub>\n", p.Head.SHA, rev.N)
	if err := c.comment(ctx, p.Number, b.String()); err != nil {
		return state, fmt.Errorf("comment: %w", err)
	}
	return state, nil
}

// comment creates fausto's comment on pull request n, or updates it.
func (c *Checker) comment(ctx context.Context, n int, body string) error {
	var comments []comment
	if err := c.GH.Call(ctx, "GET", fmt.Sprintf("/repos/%s/issues/%d/comments?per_page=100", c.Repo, n), nil, &comments); err != nil {
		return err
	}
	for _, cm := range comments {
		if strings.HasPrefix(cm.Body, marker) {
			return c.GH.Call(ctx, "PATCH", fmt.Sprintf("/repos/%s/issues/comments/%d", c.Repo, cm.ID), map[string]string{"body": body}, nil)
		}
	}
	return c.GH.Call(ctx, "POST", fmt.Sprintf("/repos/%s/issues/%d/comments", c.Repo, n), map[string]string{"body": body}, nil)
}

// fetch brings pull request p's head into the bare clone and extracts it
// into a new temporary directory.
func (c *Checker) fetch(ctx context.Context, p pull) (string, func(), error) {
	if _, err := os.Stat(filepath.Join(c.Clone, "HEAD")); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(c.Clone, 0o755); err != nil {
			return "", nil, err
		}
		if _, err := c.git(ctx, nil, "init", "--quiet", "--bare"); err != nil {
			return "", nil, err
		}
	}
	if _, err := c.git(ctx, github.GitAuthEnv(c.GH.Token), "fetch", "--quiet", "--no-tags", c.remote(), fmt.Sprintf("+refs/pull/%d/head:refs/pull/%d", p.Number, p.Number)); err != nil {
		return "", nil, err
	}
	data, err := c.git(ctx, nil, "archive", "--format=tar", p.Head.SHA)
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp("", "fausto-pr-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	if err := untar(dir, data); err != nil {
		cleanup()
		return "", nil, err
	}
	return dir, cleanup, nil
}

// remote is the project's repository URL.
func (c *Checker) remote() string {
	if c.Remote != "" {
		return c.Remote
	}
	return "https://github.com/" + c.Repo + ".git"
}

func (c *Checker) git(ctx context.Context, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "safe.directory=*"}, args...)...)
	cmd.Dir = c.Clone
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// untar extracts the regular files of a tar archive under dir.
func untar(dir string, data []byte) error {
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		dst := filepath.Join(dir, filepath.FromSlash(h.Name))
		if !strings.HasPrefix(dst, dir+string(filepath.Separator)) {
			continue // never write outside dir
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, tr)
		f.Close()
		if err != nil {
			return err
		}
	}
}
