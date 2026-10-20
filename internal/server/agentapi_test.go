package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JuanVizcaya/fausto/internal/proposal"
	"github.com/JuanVizcaya/fausto/internal/source"
	"github.com/JuanVizcaya/fausto/internal/store"
	"github.com/JuanVizcaya/fausto/internal/store/memstore"
)

func TestAgentAPI(t *testing.T) {
	ctx := context.Background()
	root := tempRepo(t)
	reg := memstore.NewProjects()
	db := reg.Store("example")
	if err := db.CreateTasks(ctx, []store.Task{
		{ID: "t--platform", Agent: "platform", Status: "open", Created: "2026-10-06", Title: "Build", Body: "Do.\n"},
		{ID: "t--residents", Agent: "residents", Status: "open", Created: "2026-10-06", Title: "Model", Body: "Do.\n"},
	}); err != nil {
		t.Fatal(err)
	}
	secret, tok, _ := store.NewToken("example", "platform", "")
	if err := reg.CreateToken(ctx, tok); err != nil {
		t.Fatal(err)
	}
	other, otherTok, _ := store.NewToken("elsewhere", "platform", "")
	reg.CreateToken(ctx, otherTok)

	ps := &proposal.Store{Src: &source.Local{Path: root, Branch: "main"}, Author: "Test <test@example.com>", DB: db}
	h := Multi([]Project{{ID: "example", Handler: New(Options{Project: "example", Tokens: reg, Root: root, Proposals: ps})}}, reg)
	call := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	for _, token := range []string{"", "fausto_wrong", other} {
		if rec := call("GET", "/api/agent/tasks", token, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("token %q: status %d, want 401", token, rec.Code)
		}
	}
	var tasks []store.Task
	rec := call("GET", "/api/agent/tasks?status=open", secret, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &tasks); err != nil || len(tasks) != 1 || tasks[0].ID != "t--platform" || tasks[0].Body == "" {
		t.Errorf("tasks = %s", rec.Body)
	}
	var def map[string]string
	rec = call("GET", "/api/agent/definition", secret, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &def); err != nil || def["agent"] != "platform" ||
		!strings.Contains(def["prompt"], "You are the **Platform agent**") || strings.HasPrefix(def["prompt"], "---") || def["description"] == "" {
		t.Errorf("definition = %s", rec.Body)
	}
	if rec := call("POST", "/api/agent/tasks/t--residents/status", secret, `{"status":"done"}`); rec.Code != http.StatusForbidden {
		t.Errorf("another agent's task: status %d, want 403", rec.Code)
	}
	if rec := call("POST", "/api/agent/tasks/t--platform/status", secret, `{"status":"in-progress"}`); rec.Code != http.StatusOK {
		t.Errorf("own task: status %d: %s", rec.Code, rec.Body)
	}
	got, _ := db.Task(ctx, "t--platform")
	if got.Status != "in-progress" || len(got.History) != 1 || !strings.HasPrefix(got.History[0].By, "agent platform (fausto_") {
		t.Errorf("task = %+v", got)
	}
	if rec := call("GET", "/api/agent/spec", secret, ""); !strings.Contains(rec.Body.String(), `"files":{}`) {
		t.Errorf("spec (kept in the repository) = %s", rec.Body)
	}
	db.AddSpecRevision(ctx, &store.SpecRevision{N: 1, Files: []store.SpecFile{{Path: "system/system.yaml", Content: "name: x\n"}}})
	if rec := call("GET", "/api/agent/spec", secret, ""); !strings.Contains(rec.Body.String(), `"revision":1`) || !strings.Contains(rec.Body.String(), `"system/system.yaml":"name: x\n"`) {
		t.Errorf("spec (kept in fausto) = %s", rec.Body)
	}
	if err := reg.DeleteToken(ctx, tok.Label); err != nil {
		t.Fatal(err)
	}
	if rec := call("GET", "/api/agent/me", secret, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("revoked token: status %d", rec.Code)
	}
}
