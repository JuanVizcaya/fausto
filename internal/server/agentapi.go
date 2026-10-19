package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/JuanVizcaya/fausto/internal/agentdef"
	"github.com/JuanVizcaya/fausto/internal/proposal"
	"github.com/JuanVizcaya/fausto/internal/spec"
	"github.com/JuanVizcaya/fausto/internal/store"
)

// Tokens finds the token an agent presents.
type Tokens interface {
	TokenByHash(ctx context.Context, hash string) (*store.Token, error)
}

// bearer returns the token in r's Authorization header, or "".
func bearer(r *http.Request) string {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(token)
}

// errUnauthorized is the answer to a missing, unknown or revoked token.
var errUnauthorized = errors.New("a valid agent token is needed: create one with `fausto token create <agent>`, then `fausto connect`")

// registerAgentAPI serves the agents: each request carries the token of one
// agent of this project, and sees only that agent's definition and tasks.
// Unlike the UI's API, which only listens on localhost for the people using
// it, this one asks for a token, so agents need nothing else (no database).
func registerAgentAPI(mux *http.ServeMux, project, root string, ps *proposal.Store, tokens Tokens, js *jiraSync) {
	handle := func(pattern string, h func(w http.ResponseWriter, r *http.Request, t *store.Token)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if ps == nil || tokens == nil {
				writeError(w, http.StatusServiceUnavailable, errors.New("fausto has no database, so no agent tokens or tasks"))
				return
			}
			secret := bearer(r)
			if secret == "" {
				writeError(w, http.StatusUnauthorized, errUnauthorized)
				return
			}
			t, err := tokens.TokenByHash(r.Context(), store.HashToken(secret))
			if err != nil || t.Project != project {
				writeError(w, http.StatusUnauthorized, errUnauthorized)
				return
			}
			h(w, r, t)
		})
	}
	loadSpec := func(r *http.Request) (*spec.Spec, error) {
		opts, err := ps.SpecOptions(r.Context())
		if err != nil {
			return nil, err
		}
		return spec.LoadWith(root, opts)
	}
	charterOf := func(s *spec.Spec, id string) *spec.Agent {
		if i := slices.IndexFunc(s.Agents, func(a spec.Agent) bool { return a.ID == id }); i >= 0 {
			return &s.Agents[i]
		}
		return nil
	}

	handle("GET /api/agent/me", func(w http.ResponseWriter, r *http.Request, t *store.Token) {
		out := map[string]string{"project": t.Project, "agent": t.Agent, "token": t.Label}
		if s, err := loadSpec(r); err == nil {
			if a := charterOf(s, t.Agent); a != nil {
				out["name"], out["kind"] = a.Name, a.Kind
			}
		}
		writeJSON(w, http.StatusOK, out)
	})
	handle("GET /api/agent/definition", func(w http.ResponseWriter, r *http.Request, t *store.Token) {
		s, err := loadSpec(r)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err)
			return
		}
		a := charterOf(s, t.Agent)
		if a == nil {
			writeError(w, http.StatusNotFound, errors.New("agent "+t.Agent+" has no charter any more"))
			return
		}
		description, prompt := agentdef.Parts(s, a)
		writeJSON(w, http.StatusOK, map[string]string{
			"agent": a.ID, "description": description, "prompt": prompt, "markdown": string(agentdef.Render(s, a)),
		})
	})
	handle("GET /api/agent/charters", func(w http.ResponseWriter, r *http.Request, t *store.Token) {
		// The charters fausto keeps, for checks run where the agent works;
		// none when the project keeps its own.
		opts, err := ps.SpecOptions(r.Context())
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		out := map[string]string{}
		for id, data := range opts.Charters {
			out[id] = string(data)
		}
		writeJSON(w, http.StatusOK, map[string]any{"charters": out})
	})
	handle("GET /api/agent/tasks", func(w http.ResponseWriter, r *http.Request, t *store.Token) {
		f := store.TaskFilter{Agent: t.Agent}
		if st := r.URL.Query().Get("status"); st != "" {
			f.Statuses = strings.Split(st, ",")
		}
		tasks, err := ps.DB.Tasks(r.Context(), f)
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, tasks)
	})
	handle("POST /api/agent/tasks/{id}/status", func(w http.ResponseWriter, r *http.Request, t *store.Token) {
		var req struct {
			Status string `json:"status"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		id := r.PathValue("id")
		task, err := ps.DB.Task(r.Context(), id)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		if task.Agent != t.Agent {
			writeError(w, http.StatusForbidden, errors.New("task "+id+" belongs to "+task.Agent+", not "+t.Agent))
			return
		}
		before, err := store.SetStatus(r.Context(), ps.DB, id, req.Status, "agent "+t.Agent+" ("+t.Label+")")
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, struct {
			From     string       `json:"from"`
			To       string       `json:"to"`
			JiraSync *jiraOutcome `json:"jiraSync,omitempty"`
		}{before.Status, req.Status, js.after(r.Context(), id)})
	})
}
