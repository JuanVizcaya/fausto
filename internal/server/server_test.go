package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JuanVizcaya/fausto/internal/check"
	"github.com/JuanVizcaya/fausto/internal/proposal"
	"github.com/JuanVizcaya/fausto/internal/source"
)

// repoRoot is a snapshot of a real project (Habyta), relative to this package.
const repoRoot = "../../testdata/example"

func get(t *testing.T, path string) (*http.Response, []byte) {
	t.Helper()
	h := New(Options{Root: repoRoot, Readonly: "", Repo: ""})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Result(), rec.Body.Bytes()
}

func TestSchemas(t *testing.T) {
	res, body := get(t, "/api/schemas")
	var v map[string]json.RawMessage
	if err := json.Unmarshal(body, &v); err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
	for _, name := range []string{"system", "agent", "decision", "task"} {
		if _, ok := v[name]; !ok {
			t.Errorf("missing schema %q", name)
		}
	}
}

func TestEditingDisabledWithoutStore(t *testing.T) {
	res, _ := get(t, "/api/proposals")
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", res.StatusCode)
	}
}

func TestInfoExplainsReadOnly(t *testing.T) {
	h := New(Options{Root: repoRoot, Readonly: "git has no identity", Repo: ""})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/info", nil))
	var info struct {
		Editable       bool   `json:"editable"`
		ReadonlyReason string `json:"readonlyReason"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Editable || info.ReadonlyReason != "git has no identity" {
		t.Errorf("info = %+v, want read-only with its reason", info)
	}
}

func TestInfoReportsRepo(t *testing.T) {
	h := New(Options{Root: repoRoot, Readonly: "", Repo: "https://github.com/CMOT/habyta"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/info", nil))
	var info struct {
		Repo string `json:"repo"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Repo != "https://github.com/CMOT/habyta" {
		t.Errorf("info.repo = %q, want the configured repo", info.Repo)
	}

	res, body := get(t, "/api/info") // repo unset in the get() helper
	var noRepo map[string]json.RawMessage
	if err := json.Unmarshal(body, &noRepo); err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
	if _, ok := noRepo["repo"]; ok {
		t.Errorf("info = %s, want no repo key when unset", body)
	}
}

func TestMutationsNeedSameOriginHeader(t *testing.T) {
	// The store is never reached: requests are rejected first.
	h := New(Options{Root: repoRoot, Proposals: &proposal.Store{Src: &source.Local{Path: repoRoot, Branch: "main"}}})
	for _, path := range []string{"/api/proposals/x/reject", "/api/tasks/x/status", "/api/jira/sync"} {
		for _, tc := range []struct {
			name   string
			header string
			origin string
		}{
			{"no header", "", ""},
			{"foreign origin", "1", "https://evil.example"},
		} {
			req := httptest.NewRequest(http.MethodPost, path, nil)
			if tc.header != "" {
				req.Header.Set(requestHeader, tc.header)
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s: status %d, want 403", path, tc.name, rec.Code)
			}
		}
	}
}

func TestSpec(t *testing.T) {
	res, body := get(t, "/api/spec")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
	var v struct {
		System struct {
			Components []struct{ ID string } `json:"components"`
		} `json:"system"`
		Agents []struct {
			ID   string `json:"id"`
			File string `json:"file"`
		} `json:"agents"`
		Decisions []struct {
			ID   string `json:"id"`
			Body string `json:"body"`
		} `json:"decisions"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.System.Components) == 0 {
		t.Error("no components")
	}
	if len(v.Agents) == 0 || v.Agents[0].File == "" {
		t.Errorf("agents missing or without file: %+v", v.Agents)
	}
	if len(v.Decisions) == 0 || !strings.Contains(v.Decisions[0].Body, "## Decision") {
		t.Errorf("decisions missing or without body: %+v", v.Decisions)
	}
}

func TestCheck(t *testing.T) {
	res, body := get(t, "/api/check")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
	var v struct {
		OK      bool              `json:"ok"`
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	// Whether the repository passes is CI's `fausto check`; here, every
	// check reports.
	if len(v.Results) != len(check.All) {
		t.Errorf("report = %s", body)
	}
}

func TestCode(t *testing.T) {
	res, body := get(t, "/api/code/residents")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
	var m struct {
		Component string            `json:"component"`
		Packages  []json.RawMessage `json:"packages"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if m.Component != "residents" || len(m.Packages) == 0 {
		t.Errorf("model = %s", body)
	}
	if res, _ := get(t, "/api/code/nope"); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown component: status %d", res.StatusCode)
	}
}

func TestIndex(t *testing.T) {
	res, body := get(t, "/")
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `<div id="root">`) {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
}
