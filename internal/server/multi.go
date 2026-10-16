package server

import (
	"net/http"
)

// ProjectHeader names the project an API request is for; the UI sends it
// on every request. Without it (or the "project" query parameter), a
// request goes to the first project.
const ProjectHeader = "X-Fausto-Project"

// Project is one project a multi-project server serves.
type Project struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Source  string       `json:"source"` // a local path, or "github.com/owner/repo"
	Handler http.Handler `json:"-"`
	// Error says why the project couldn't be opened; it is listed, but its
	// requests fail with it.
	Error string `json:"error,omitempty"`
}

// Multi serves several projects: GET /api/projects lists them, and every
// other request goes to the project its ProjectHeader names.
func Multi(projects []Project) http.Handler {
	byID := map[string]*Project{}
	for i := range projects {
		byID[projects[i].ID] = &projects[i]
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/projects" && r.Method == http.MethodGet {
			list := projects
			if list == nil {
				list = []Project{}
			}
			writeJSON(w, http.StatusOK, list)
			return
		}
		if len(projects) == 0 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "fausto has no projects yet: add one with `fausto project add`"})
			return
		}
		id := r.Header.Get(ProjectHeader)
		if id == "" {
			id = r.URL.Query().Get("project")
		}
		p := &projects[0]
		if id != "" {
			if p = byID[id]; p == nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "no project " + id})
				return
			}
		}
		if p.Handler == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": p.Error})
			return
		}
		p.Handler.ServeHTTP(w, r)
	})
}
