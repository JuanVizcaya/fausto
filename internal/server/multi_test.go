package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMulti(t *testing.T) {
	named := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(name)) })
	}
	h := Multi([]Project{
		{ID: "a", Name: "A", Source: "/src/a", Handler: named("a")},
		{ID: "b", Name: "B", Source: "github.com/o/b", Handler: named("b")},
		{ID: "c", Name: "C", Error: "clone failed"},
	})
	get := func(path, project string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if project != "" {
			req.Header.Set(ProjectHeader, project)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	var list []Project
	if rec := get("/api/projects", ""); json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list) != 3 || list[2].Error != "clone failed" {
		t.Errorf("projects = %s", rec.Body)
	}
	for _, c := range []struct{ path, project, want string }{
		{"/api/spec", "", "a"},
		{"/api/spec", "b", "b"},
		{"/api/spec?project=b", "", "b"},
		{"/", "b", "b"},
	} {
		if rec := get(c.path, c.project); rec.Body.String() != c.want {
			t.Errorf("%s for %q went to %q", c.path, c.project, rec.Body)
		}
	}
	if rec := get("/api/spec", "nope"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project: %d", rec.Code)
	}
	if rec := get("/api/spec", "c"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("broken project: %d", rec.Code)
	}
}
