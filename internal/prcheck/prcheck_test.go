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
	mu       sync.Mutex
	sha      string
	statuses []map[string]string
	comments []map[string]any
	edits    int
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
	default:
		http.Error(w, `{"message":"unexpected `+r.Method+" "+r.URL.Path+`"}`, http.StatusTeapot)
	}
}

func TestRun(t *testing.T) {
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
	defer api.Close()
	c := &Checker{Repo: "o/r", GH: &github.Client{API: api.URL}, DB: db, Clone: filepath.Join(t.TempDir(), "prs.git"), Remote: remote}

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
}

func TestNothingToCheckWithoutAKeptSpec(t *testing.T) {
	c := &Checker{Repo: "o/r", GH: &github.Client{API: "http://127.0.0.1:1"}, DB: memstore.New()}
	if res, err := c.Run(context.Background()); err != nil || res != nil {
		t.Errorf("run = %+v, %v", res, err)
	}
}
