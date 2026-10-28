package prcheck

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/JuanVizcaya/fausto/internal/check"
	"github.com/JuanVizcaya/fausto/internal/github"
	"github.com/JuanVizcaya/fausto/internal/spec"
	"github.com/JuanVizcaya/fausto/internal/store"
	"github.com/JuanVizcaya/fausto/internal/store/memstore"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=T", "-c", "user.email=t@example.com"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// fake is the GitHub API, as much as Checker uses: one open pull request.
type fake struct {
	mu         sync.Mutex
	sha        string
	statuses   []map[string]string
	comments   []map[string]any
	edits      int
	dispatches []map[string]any
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	switch {
	case r.Method == "GET" && r.URL.Path == "/repos/o/r/pulls":
		json.NewEncoder(w).Encode([]map[string]any{{"number": 1, "head": map[string]string{"sha": f.sha}}})
	case r.Method == "GET" && r.URL.Path == "/repos/o/r/commits/"+f.sha+"/statuses":
		var newest []map[string]string
		for i := len(f.statuses) - 1; i >= 0; i-- {
			newest = append(newest, f.statuses[i])
		}
		json.NewEncoder(w).Encode(newest)
	case r.Method == "POST" && r.URL.Path == "/repos/o/r/statuses/"+f.sha:
		var s map[string]string
		json.Unmarshal(body, &s)
		f.statuses = append(f.statuses, s)
		w.WriteHeader(http.StatusCreated)
	case r.Method == "GET" && r.URL.Path == "/repos/o/r/issues/1/comments":
		json.NewEncoder(w).Encode(f.comments)
	case r.Method == "POST" && r.URL.Path == "/repos/o/r/issues/1/comments":
		var c map[string]any
		json.Unmarshal(body, &c)
		c["id"] = len(f.comments) + 1
		f.comments = append(f.comments, c)
		w.WriteHeader(http.StatusCreated)
	case r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/repos/o/r/issues/comments/"):
		var c map[string]any
		json.Unmarshal(body, &c)
		f.comments[0]["body"] = c["body"]
		f.edits++
	case r.Method == "POST" && r.URL.Path == "/repos/o/r/dispatches":
		var d map[string]any
		json.Unmarshal(body, &d)
		f.dispatches = append(f.dispatches, d)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, `{"message":"unexpected `+r.Method+" "+r.URL.Path+`"}`, http.StatusTeapot)
	}
}

// setup returns a Checker for one pull request of the example's code, whose
// spec, revision 1, is in the database, and the fake GitHub behind it.
func setup(t *testing.T) (*Checker, *fake, map[string][]byte) {
	t.Helper()
	ctx := context.Background()
	// The project: the example's code without system/, whose spec is in
	// the database.
	src := "../../testdata/example"
	files, err := check.ListFiles(src)
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	for _, f := range files {
		if strings.HasPrefix(f, "system/") {
			continue
		}
		data, _ := os.ReadFile(filepath.Join(src, f))
		os.MkdirAll(filepath.Dir(filepath.Join(work, f)), 0o755)
		os.WriteFile(filepath.Join(work, f), data, 0o644)
	}
	git(t, work, "init", "-q", "-b", "main")
	git(t, work, "add", "-A")
	git(t, work, "commit", "-q", "-m", "a pull request")
	sha := git(t, work, "rev-parse", "HEAD")
	remote := filepath.Join(t.TempDir(), "r.git")
	git(t, work, "init", "-q", "--bare", remote)
	git(t, work, "push", "-q", remote, "HEAD:refs/pull/1/head")

	db := memstore.New()
	specFiles, _ := spec.ReadDir(src)
	db.AddSpecRevision(ctx, &store.SpecRevision{N: 1, Files: store.SpecFiles(specFiles)})
	gh := &fake{sha: sha}
	api := httptest.NewServer(gh)
	t.Cleanup(api.Close)
	c := &Checker{Repo: "o/r", GH: &github.Client{API: api.URL}, DB: db, Clone: filepath.Join(t.TempDir(), "prs.git"), Remote: remote}
	return c, gh, specFiles
}

func TestRun(t *testing.T) {
	ctx := context.Background()
	c, gh, specFiles := setup(t)
	db := c.DB.(*memstore.Store)

	res, err := c.Run(ctx)
	if err != nil || len(res) != 1 || res[0].Error != "" || res[0].State == "" {
		t.Fatalf("run = %+v, %v", res, err)
	}
	if len(gh.statuses) != 1 || gh.statuses[0]["context"] != Context || !strings.HasSuffix(gh.statuses[0]["description"], " · spec r1") ||
		gh.statuses[0]["state"] != res[0].State {
		t.Errorf("statuses = %+v", gh.statuses)
	}
	if len(gh.comments) != 1 || !strings.HasPrefix(gh.comments[0]["body"].(string), marker) || !strings.Contains(gh.comments[0]["body"].(string), "spec revision 1") {
		t.Errorf("comments = %+v", gh.comments)
	}

	// Checked already at this revision: nothing new.
	if res, _ := c.Run(ctx); len(res) != 1 || !res[0].Skipped || len(gh.statuses) != 1 {
		t.Errorf("second run = %+v, statuses %d", res, len(gh.statuses))
	}

	// A new revision re-checks it, updating the same comment.
	db.AddSpecRevision(ctx, &store.SpecRevision{N: 2, Files: store.SpecFiles(specFiles)})
	if res, err := c.Run(ctx); err != nil || res[0].Skipped || res[0].Revision != 2 {
		t.Errorf("run after a new revision = %+v, %v", res, err)
	}
	if len(gh.statuses) != 2 || len(gh.comments) != 1 || gh.edits != 1 || !strings.Contains(gh.comments[0]["body"].(string), "spec revision 2") {
		t.Errorf("statuses %d, comments %d, edits %d", len(gh.statuses), len(gh.comments), gh.edits)
	}
	if len(gh.dispatches) != 0 {
		t.Errorf("dispatched without HandOff: %+v", gh.dispatches)
	}
}

func TestHandOff(t *testing.T) {
	ctx := context.Background()
	c, gh, specFiles := setup(t)
	c.HandOff = true
	if res, err := c.Run(ctx); err != nil || res[0].Error != "" {
		t.Fatalf("run = %+v, %v", res, err)
	}
	if len(gh.dispatches) != 1 || len(gh.statuses) != 1 {
		t.Fatalf("dispatches %+v, statuses %d", gh.dispatches, len(gh.statuses))
	}
	d := gh.dispatches[0]
	data, _ := json.Marshal(d["client_payload"])
	var payload Dispatch
	json.Unmarshal(data, &payload)
	if d["event_type"] != EventType || payload != (Dispatch{PR: 1, HeadSHA: gh.sha, Revision: 1, SpecRef: "refs/fausto/spec/r1"}) {
		t.Errorf("dispatch = %+v", d)
	}
	// The ref holds exactly the spec's files.
	listed := git(t, c.Remote, "ls-tree", "-r", "--name-only", "refs/fausto/spec/r1")
	var paths []string
	for p := range specFiles {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	if listed != strings.Join(paths, "\n") {
		t.Errorf("spec ref holds:\n%s\nwant:\n%s", listed, strings.Join(paths, "\n"))
	}
	if got := git(t, c.Remote, "show", "refs/fausto/spec/r1:system/system.yaml"); got != strings.TrimSpace(string(specFiles["system/system.yaml"])) {
		t.Errorf("system.yaml differs")
	}
	// Publishing the same revision again is a no-op: the commit is the same.
	first := git(t, c.Remote, "rev-parse", "refs/fausto/spec/r1")
	rev, err := c.DB.SpecRevision(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := c.specCommit(ctx, rev)
	if err != nil || commit != first {
		t.Errorf("spec commit %q, %v; want %q", commit, err, first)
	}
}

func TestFailedHandOffLeavesThePullRequestUnchecked(t *testing.T) {
	ctx := context.Background()
	c, gh, _ := setup(t)
	c.HandOff = true
	// The repository refuses the spec ref, as without contents write.
	hook := filepath.Join(c.Remote, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho refused >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := c.Run(ctx)
	if err != nil || !strings.Contains(res[0].Error, "hand off the spec") || len(gh.statuses) != 0 || len(gh.dispatches) != 0 {
		t.Fatalf("run = %+v, %v; statuses %d, dispatches %d", res, err, len(gh.statuses), len(gh.dispatches))
	}
	// Once the repository accepts it, the next run checks the pull request.
	os.Remove(hook)
	if res, err := c.Run(ctx); err != nil || res[0].Error != "" || res[0].Skipped || len(gh.statuses) != 1 || len(gh.dispatches) != 1 {
		t.Fatalf("retry = %+v, %v; statuses %d, dispatches %d", res, err, len(gh.statuses), len(gh.dispatches))
	}
}

func TestNothingToCheckWithoutAKeptSpec(t *testing.T) {
	c := &Checker{Repo: "o/r", GH: &github.Client{API: "http://127.0.0.1:1"}, DB: memstore.New()}
	if res, err := c.Run(context.Background()); err != nil || res != nil {
		t.Errorf("run = %+v, %v", res, err)
	}
}
