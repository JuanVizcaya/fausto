package source

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GitHub is a repository on GitHub. Fausto keeps its own clone of it in
// Dir, checked out (detached) at the base branch, and refreshes it on
// Fetch. Accepting a proposal pushes its branch and opens a pull request;
// the pull request's state says when it is merged or closed.
type GitHub struct {
	Repo   string // "owner/repo"
	Branch string // the base branch; empty means the repository's default
	// Token authenticates git and the API: a fine-grained token with
	// contents and pull requests read/write on the repository. Fausto
	// passes it in each git command's environment; it is never written to
	// the clone's configuration.
	Token string
	// Clone is the directory of fausto's clone.
	Clone string
	// API and Remote override https://api.github.com and the repository's
	// https URL, for tests.
	API    string
	Remote string
	HTTP   *http.Client
}

// OpenGitHub returns the repository, cloning it into clone the first time
// and fetching it otherwise.
func OpenGitHub(ctx context.Context, g *GitHub) (*GitHub, error) {
	if g.API == "" {
		g.API = "https://api.github.com"
	}
	if g.Remote == "" {
		g.Remote = "https://github.com/" + g.Repo + ".git"
	}
	if g.HTTP == nil {
		g.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if g.Branch == "" {
		var repo struct {
			DefaultBranch string `json:"default_branch"`
		}
		if err := g.call(ctx, http.MethodGet, "/repos/"+g.Repo, nil, &repo); err != nil {
			return nil, fmt.Errorf("find %s's default branch: %w", g.Repo, err)
		}
		g.Branch = repo.DefaultBranch
	}
	if _, err := os.Stat(filepath.Join(g.Clone, ".git")); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(g.Clone), 0o755); err != nil {
			return nil, err
		}
		if _, err := git(ctx, filepath.Dir(g.Clone), g.auth(), "clone", "--quiet", "--no-checkout", g.Remote, g.Clone); err != nil {
			return nil, err
		}
	}
	return g, g.Fetch(ctx)
}

func (g *GitHub) Dir() string     { return g.Clone }
func (g *GitHub) Base() string    { return g.Branch }
func (g *GitHub) BaseRef() string { return "refs/remotes/origin/" + g.Branch }
func (g *GitHub) Web() string     { return "https://github.com/" + g.Repo }

// Fetch updates the clone and checks out the base branch, discarding
// anything in the work tree: the clone is fausto's alone.
func (g *GitHub) Fetch(ctx context.Context) error {
	if _, err := git(ctx, g.Clone, g.auth(), "fetch", "--quiet", "--prune", "origin"); err != nil {
		return err
	}
	if _, err := git(ctx, g.Clone, nil, "checkout", "--quiet", "--detach", "--force", g.BaseRef()); err != nil {
		return err
	}
	_, err := git(ctx, g.Clone, nil, "clean", "-dfxq")
	return err
}

// auth is the environment that gives git the token as an HTTP header for
// this command only.
func (g *GitHub) auth() []string {
	if g.Token == "" {
		return nil
	}
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + g.Token))
	return []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraHeader", "GIT_CONFIG_VALUE_0=Authorization: Basic " + basic,
		"GIT_TERMINAL_PROMPT=0"}
}

type pull struct {
	Number         int    `json:"number"`
	HTMLURL        string `json:"html_url"`
	State          string `json:"state"`
	Merged         bool   `json:"merged"`
	MergeCommitSHA string `json:"merge_commit_sha"`
	Head           struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

// Publish pushes commit to branch and opens a pull request into the base
// branch, or finds the one a retried accept already opened.
func (g *GitHub) Publish(ctx context.Context, branch, commit, title, body string) (*Review, error) {
	if _, err := git(ctx, g.Clone, g.auth(), "push", "--quiet", "origin", commit+":refs/heads/"+branch); err != nil {
		return nil, err
	}
	owner, _, _ := strings.Cut(g.Repo, "/")
	var existing []pull
	q := url.Values{"head": {owner + ":" + branch}, "state": {"open"}}
	if err := g.call(ctx, http.MethodGet, "/repos/"+g.Repo+"/pulls?"+q.Encode(), nil, &existing); err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		return &Review{Branch: branch, Commit: commit, PR: existing[0].Number, URL: existing[0].HTMLURL}, nil
	}
	var p pull
	req := map[string]any{"title": title, "head": branch, "base": g.Branch, "body": body, "maintainer_can_modify": true}
	if err := g.call(ctx, http.MethodPost, "/repos/"+g.Repo+"/pulls", req, &p); err != nil {
		return nil, fmt.Errorf("open a pull request: %w", err)
	}
	return &Review{Branch: branch, Commit: commit, PR: p.Number, URL: p.HTMLURL}, nil
}

// Status is the pull request's state: merged (in any way: merge, squash or
// rebase), closed without merging, or open.
func (g *GitHub) Status(ctx context.Context, r Review) (*Outcome, error) {
	if r.PR == 0 {
		return nil, errors.New("no pull request recorded")
	}
	var p pull
	if err := g.call(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d", g.Repo, r.PR), nil, &p); err != nil {
		return nil, err
	}
	switch {
	case p.Merged:
		return &Outcome{State: Merged, Merged: p.MergeCommitSHA}, nil
	case p.State == "closed":
		return &Outcome{State: Closed}, nil
	}
	return &Outcome{State: Open}, nil
}

// APIError is an unsuccessful GitHub API response.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("GitHub API: %d %s", e.Status, e.Message) }

func (g *GitHub) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(g.API, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	res, err := g.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Message == "" {
			e.Message = http.StatusText(res.StatusCode)
		}
		return &APIError{Status: res.StatusCode, Message: e.Message}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}
