package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"

	"github.com/JuanVizcaya/fausto/internal/edit"
	"github.com/JuanVizcaya/fausto/internal/proposal"
	"github.com/JuanVizcaya/fausto/internal/source"
	"github.com/JuanVizcaya/fausto/internal/spec"
	"github.com/JuanVizcaya/fausto/internal/store"
)

// requestHeader must be set on every mutating request. Browsers only send
// custom headers cross-origin after a CORS preflight, which this server
// never approves, so other sites cannot forge these requests.
const requestHeader = "X-Fausto-Request"

func registerProposals(mux *http.ServeMux, root string, ps *proposal.Store, js *jiraSync, sy *syncer) {
	handle := func(pattern string, h func(w http.ResponseWriter, r *http.Request)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if ps == nil {
				writeError(w, http.StatusServiceUnavailable, errors.New("editing is unavailable: fausto needs a git repository with a base branch and a database (FAUSTO_MONGO_URI)"))
				return
			}
			if r.Method == http.MethodPost && !sameOrigin(r) {
				writeError(w, http.StatusForbidden, errors.New("cross-origin request rejected"))
				return
			}
			h(w, r)
		})
	}
	handle("GET /api/proposals", func(w http.ResponseWriter, r *http.Request) {
		// Notice merged and closed reviews when someone looks, at most
		// every few seconds.
		sy.maybe(r.Context())
		list, err := ps.List(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, list)
	})
	handle("POST /api/proposals", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Title       string        `json:"title"`
			Description string        `json:"description"`
			Milestone   string        `json:"milestone"`
			Changes     []edit.Change `json:"changes"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		d, err := ps.Create(r.Context(), req.Title, req.Description, req.Milestone, req.Changes)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, d)
	})
	handle("GET /api/proposals/{id}", func(w http.ResponseWriter, r *http.Request) {
		d, err := ps.Detail(r.Context(), r.PathValue("id"))
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, d)
	})
	handle("POST /api/proposals/sync", func(w http.ResponseWriter, r *http.Request) {
		results, jo, err := sy.run(r.Context())
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Results  []proposal.SyncResult `json:"results"`
			JiraSync *jiraOutcome          `json:"jiraSync,omitempty"`
		}{nonNilResults(results), jo})
	})
	handle("POST /api/proposals/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		p, err := ps.Approve(r.Context(), r.PathValue("id"))
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, p)
	})
	handle("POST /api/proposals/{id}/reject", func(w http.ResponseWriter, r *http.Request) {
		p, err := ps.Reject(r.Context(), r.PathValue("id"))
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, p)
	})
	handle("POST /api/tasks/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Status string `json:"status"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		// Agents set their own tasks' status with `fausto task`; people set
		// theirs here.
		id := r.PathValue("id")
		t, err := ps.DB.Task(r.Context(), id)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		opts, err := ps.SpecOptions(r.Context())
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		sp, err := spec.LoadWith(root, opts)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err)
			return
		}
		if i := slices.IndexFunc(sp.Agents, func(a spec.Agent) bool { return a.ID == t.Agent }); i < 0 || sp.Agents[i].Kind != "human" {
			writeError(w, http.StatusConflict, errors.New("Only tasks for people can be set here; agents set their own with `fausto task`."))
			return
		}
		if _, err := store.SetStatus(r.Context(), ps.DB, id, req.Status, ps.Who()); err != nil {
			writeStoreError(w, err)
			return
		}
		if t, err = ps.DB.Task(r.Context(), id); err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, struct {
			*store.Task
			JiraSync *jiraOutcome `json:"jiraSync,omitempty"`
		}{t, js.after(r.Context(), t.ID)})
	})
	handle("GET /api/jira/issues", func(w http.ResponseWriter, r *http.Request) {
		if js == nil {
			writeError(w, http.StatusServiceUnavailable, errors.New("Jira is not configured: set the FAUSTO_JIRA_* variables and restart fausto"))
			return
		}
		issues, err := js.issues(r.Context())
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, issues)
	})
	handle("POST /api/jira/sync", func(w http.ResponseWriter, r *http.Request) {
		if js == nil {
			writeError(w, http.StatusServiceUnavailable, errors.New("Jira is not configured: set the FAUSTO_JIRA_* variables and restart fausto"))
			return
		}
		report, err := js.run(r.Context())
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "report": report})
			return
		}
		writeJSON(w, http.StatusOK, report)
	})
}

func sameOrigin(r *http.Request) bool {
	if r.Header.Get(requestHeader) == "" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

func writeStoreError(w http.ResponseWriter, err error) {
	var invalid *proposal.InvalidError
	var conflict *proposal.ConflictError
	var api *source.APIError
	switch {
	case errors.As(err, &api):
		writeError(w, http.StatusBadGateway, err)
	case errors.Is(err, proposal.ErrNotFound), errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.As(err, &invalid):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "findings": invalid.Findings})
	case errors.As(err, &conflict), errors.Is(err, store.ErrChanged):
		writeError(w, http.StatusConflict, err)
	default:
		writeError(w, http.StatusBadRequest, err)
	}
}
