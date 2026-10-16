package source

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var ctx = context.Background()

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := git(ctx, dir, []string{"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com"}, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// newRepo returns a repository on branch main with one commit.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.txt", "a\n")
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-q", "-m", "initial")
	return dir
}

func write(t *testing.T, dir, name, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// commitOff makes a commit on top of main without touching the work tree,
// as fausto does, and returns it.
func commitOff(t *testing.T, dir string) string {
	t.Helper()
	tree := run(t, dir, "rev-parse", "main^{tree}")
	return run(t, dir, "commit-tree", tree, "-p", "main", "-m", "proposal")
}

func TestLocal(t *testing.T) {
	dir := newRepo(t)
	l, err := NewLocal(ctx, dir, "")
	if err != nil || l.Base() != "main" {
		t.Fatalf("NewLocal = %+v, %v", l, err)
	}
	commit := commitOff(t, dir)
	r, err := l.Publish(ctx, "fausto/p", commit, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Publish(ctx, "fausto/p", commit, "", ""); err != nil {
		t.Errorf("retried publish: %v", err)
	}
	if _, err := l.Publish(ctx, "fausto/p", run(t, dir, "rev-parse", "main"), "", ""); err == nil {
		t.Error("publish over a different commit: no error")
	}
	if head := run(t, dir, "rev-parse", "HEAD"); head == commit {
		t.Error("publish moved the checked-out branch")
	}
	if o, _ := l.Status(ctx, *r); o.State != Open {
		t.Errorf("before merge: %+v", o)
	}
	run(t, dir, "merge", "-q", "--ff-only", "fausto/p")
	if o, _ := l.Status(ctx, *r); o.State != Merged {
		t.Errorf("after merge: %+v", o)
	}

	other := commitOff(t, dir)
	r2, err := l.Publish(ctx, "fausto/q", other, "", "")
	if err != nil {
		t.Fatal(err)
	}
	run(t, dir, "branch", "-q", "-D", "fausto/q")
	if o, _ := l.Status(ctx, *r2); o.State != Closed {
		t.Errorf("deleted branch: %+v", o)
	}
}

// fakeGitHub serves the few API calls GitHub makes.
type fakeGitHub struct {
	mu    sync.Mutex
	pulls map[int]*pull
	auth  []string
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r":
		json.NewEncoder(w).Encode(map[string]string{"default_branch": "main"})
	case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls":
		var out []*pull
		for _, p := range f.pulls {
			if p.State == "open" && r.URL.Query().Get("head") == "o:"+p.Head.SHA {
				out = append(out, p)
			}
		}
		json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/pulls":
		var in struct{ Head, Base, Title string }
		json.NewDecoder(r.Body).Decode(&in)
		n := len(f.pulls) + 1
		p := &pull{Number: n, HTMLURL: "https://github.com/o/r/pull/" + string(rune('0'+n)), State: "open"}
		p.Head.SHA = in.Head // the fake keys pulls by branch
		f.pulls[n] = p
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(p)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/o/r/pulls/"):
		var n int
		for _, c := range strings.TrimPrefix(r.URL.Path, "/repos/o/r/pulls/") {
			n = n*10 + int(c-'0')
		}
		p := f.pulls[n]
		if p == nil {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(p)
	default:
		http.Error(w, `{"message":"unexpected"}`, http.StatusTeapot)
	}
}

func TestGitHub(t *testing.T) {
	upstream := newRepo(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	run(t, upstream, "clone", "-q", "--bare", upstream, remote)
	fake := &fakeGitHub{pulls: map[int]*pull{}}
	api := httptest.NewServer(fake)
	defer api.Close()

	clone := filepath.Join(t.TempDir(), "repos", "r")
	g, err := OpenGitHub(ctx, &GitHub{Repo: "o/r", Token: "secret", Clone: clone, API: api.URL, Remote: remote})
	if err != nil {
		t.Fatal(err)
	}
	if g.Base() != "main" {
		t.Errorf("base = %q", g.Base())
	}
	if data, err := os.ReadFile(filepath.Join(clone, "a.txt")); err != nil || string(data) != "a\n" {
		t.Errorf("checked out a.txt = %q, %v", data, err)
	}
	if cfg, _ := os.ReadFile(filepath.Join(clone, ".git", "config")); strings.Contains(string(cfg), "extraHeader") {
		t.Error("the token's header was written to the clone's config")
	}

	tree := run(t, clone, "rev-parse", g.BaseRef()+"^{tree}")
	commit := run(t, clone, "commit-tree", tree, "-p", g.BaseRef(), "-m", "proposal")
	r, err := g.Publish(ctx, "fausto/p", commit, "Title", "Body")
	if err != nil {
		t.Fatal(err)
	}
	if r.PR != 1 || r.URL == "" {
		t.Errorf("review = %+v", r)
	}
	if got := run(t, remote, "rev-parse", "refs/heads/fausto/p"); got != commit {
		t.Errorf("pushed %s, want %s", got, commit)
	}
	if again, err := g.Publish(ctx, "fausto/p", commit, "Title", "Body"); err != nil || again.PR != 1 {
		t.Errorf("retried publish = %+v, %v (want the same pull request)", again, err)
	}
	if o, err := g.Status(ctx, *r); err != nil || o.State != Open {
		t.Errorf("open: %+v, %v", o, err)
	}
	fake.mu.Lock()
	fake.pulls[1].Merged, fake.pulls[1].State, fake.pulls[1].MergeCommitSHA = true, "closed", "abc"
	fake.mu.Unlock()
	if o, err := g.Status(ctx, *r); err != nil || o.State != Merged || o.Merged != "abc" {
		t.Errorf("merged: %+v, %v", o, err)
	}
	for _, a := range fake.auth {
		if a != "Bearer secret" {
			t.Errorf("API call with Authorization %q", a)
		}
	}

	// A change on the remote shows up after Fetch.
	run(t, upstream, "push", "-q", remote, "main")
	write(t, upstream, "b.txt", "b\n")
	run(t, upstream, "add", "-A")
	run(t, upstream, "commit", "-q", "-m", "more")
	run(t, upstream, "push", "-q", remote, "main")
	if err := g.Fetch(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(clone, "b.txt")); err != nil {
		t.Errorf("after fetch: %v", err)
	}
}
