package server

import (
	"net/http"
	"strings"

	"github.com/JuanVizcaya/fausto/internal/store"
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

// Multi serves several projects: GET /api/projects lists them, a request
// to the agent API goes to the project of its token (found with tokens),
// and every other request to the project its ProjectHeader names.
func Multi(projects []Project, tokens Tokens) http.Handler {
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
		if strings.HasPrefix(r.URL.Path, "/api/agent/") {
			// The token says which project; the project checks it again.
			id = ""
			if secret := bearer(r); secret != "" && tokens != nil {
				if t, err := tokens.TokenByHash(r.Context(), store.HashToken(secret)); err == nil {
					id = t.Project
				}
			}
			if byID[id] == nil {
				writeError(w, http.StatusUnauthorized, errUnauthorized)
				return
			}
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
