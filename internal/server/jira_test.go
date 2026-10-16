package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JuanVizcaya/fausto/internal/check"
	"github.com/JuanVizcaya/fausto/internal/edit"
	"github.com/JuanVizcaya/fausto/internal/jira"
	"github.com/JuanVizcaya/fausto/internal/proposal"
	"github.com/JuanVizcaya/fausto/internal/spec"
	"github.com/JuanVizcaya/fausto/internal/store"
	"github.com/JuanVizcaya/fausto/internal/store/memstore"
)

func post(h http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set(requestHeader, "1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestJiraOffByDefault(t *testing.T) {
	h := New(repoRoot, &proposal.Store{Root: repoRoot, Base: "main"}, "", "", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if rec := post(h, "/api/jira/sync", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("sync: status %d, want 503", rec.Code)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/jira/issues", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("issues: status %d, want 503", rec.Code)
	}
	_, body := get(t, "/api/info")
	if strings.Contains(string(body), `"jira"`) {
		t.Errorf("info mentions jira: %s", body)
	}
}

// tempRepo copies the Habyta files (without proposals and tasks) into a
// new git repository on branch main.
func tempRepo(t *testing.T) string {
	t.Helper()
	files, err := check.ListFiles(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, f := range files {
		if strings.HasPrefix(f, spec.Dir+"/proposals/") || (strings.HasPrefix(f, "tasks/") && f != "tasks/README.md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(repoRoot, f))
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"-c", "user.name=T", "-c", "user.email=t@example.com", "commit", "-q", "-m", "initial"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return root
}

func TestAcceptSucceedsWhenJiraFails(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"errorMessages":["Jira is down"]}`)
	}))
	defer down.Close()
	jc := jira.NewClient(jira.Config{URL: down.URL, Email: "me@example.com", Token: "t", Project: "HAB", IssueType: "Task", Statuses: map[string]string{"open": "To Do"}})

	root := tempRepo(t)
	store := &proposal.Store{Root: root, Base: "main", Author: "Test <test@example.com>", DB: memstore.New(),
		Now: func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) }}
	sp, err := spec.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	principle := sp.Principles.Principles[0]
	principle.Rationale += " Keep it that way."
	value, _ := json.Marshal(principle)
	d, err := store.Create(context.Background(), "Tighten a principle", "", "", []edit.Change{{Kind: "principle", ID: principle.ID, Value: value}})
	if err != nil {
		t.Fatal(err)
	}

	h := New(root, store, "", "", jc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec := post(h, "/api/proposals/"+d.ID+"/approve", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("approve: status %d: %s", rec.Code, rec.Body)
	}
	var res struct {
		Status string   `json:"status"`
		Tasks  []string `json:"tasks"`
		Jira   struct {
			Report jira.Report `json:"report"`
		} `json:"jiraSync"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != "accepted" || len(res.Tasks) == 0 || res.Jira.Report.Failed() != len(res.Tasks) {
		t.Errorf("response = %s", rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "Jira is down") {
		t.Errorf("response hides the Jira error: %s", rec.Body)
	}
}

// Tasks come from the database: /api/spec lists them, and only a person's
// task can have its status set from the UI.
func TestTasksFromDatabase(t *testing.T) {
	root := tempRepo(t)
	db := memstore.New()
	ps := &proposal.Store{Root: root, Base: "main", Author: "Test <test@example.com>", DB: db}
	ctx := context.Background()
	if err := db.CreateTasks(ctx, []store.Task{
		{ID: "t--orchestrator", Agent: "orchestrator", Status: "open", Created: "2026-10-06", Title: "Review", Body: "Look.\n"},
		{ID: "t--platform", Agent: "platform", Status: "open", Created: "2026-10-06", Title: "Build", Body: "Do.\n"},
	}); err != nil {
		t.Fatal(err)
	}
	h := New(root, ps, "", "", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/spec", nil))
	var v struct {
		Tasks []store.Task `json:"tasks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || len(v.Tasks) != 2 || v.Tasks[0].Body == "" {
		t.Fatalf("spec tasks = %+v, %v", v.Tasks, err)
	}

	if rec := post(h, "/api/tasks/t--orchestrator/status", `{"status":"done"}`); rec.Code != http.StatusOK {
		t.Errorf("person's task: status %d: %s", rec.Code, rec.Body)
	}
	if got, _ := db.Task(ctx, "t--orchestrator"); got.Status != "done" || len(got.History) != 1 || got.History[0].By != "Test <test@example.com>" {
		t.Errorf("task = %+v", got)
	}
	if rec := post(h, "/api/tasks/t--platform/status", `{"status":"done"}`); rec.Code != http.StatusConflict {
		t.Errorf("agent's task: status %d, want 409", rec.Code)
	}
	if rec := post(h, "/api/tasks/nope/status", `{"status":"done"}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown task: status %d, want 404", rec.Code)
	}
	if rec := post(h, "/api/tasks/t--orchestrator/status", `{"status":"finished"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad status: status %d, want 400", rec.Code)
	}
}
