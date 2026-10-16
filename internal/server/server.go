// Package server serves the fausto web UI and a JSON API over the system
// spec, the drift checks, and the code of each component. Every request
// rereads system/, so edits show up on reload.
package server

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/JuanVizcaya/fausto/internal/check"
	"github.com/JuanVizcaya/fausto/internal/code"
	"github.com/JuanVizcaya/fausto/internal/jira"
	"github.com/JuanVizcaya/fausto/internal/proposal"
	"github.com/JuanVizcaya/fausto/internal/schema"
	"github.com/JuanVizcaya/fausto/internal/spec"
	"github.com/JuanVizcaya/fausto/internal/store"
)

// dist is the built web UI (fausto/web, `npm run build`).
//
//go:embed dist
var dist embed.FS

// New returns a handler for the repository at root. proposals may be nil,
// which disables editing (for example outside a git repository); readonly
// then says why and how to enable it, for the UI to show. repo is the
// repository's GitHub URL (e.g. "https://github.com/CMOT/habyta"), for the
// UI to link into; empty when it couldn't be resolved. jc, when set, mirrors
// tasks as Jira issues.
func New(root string, proposals *proposal.Store, readonly string, repo string, jc *jira.Client, logger *slog.Logger) http.Handler {
	static, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is embedded at build time
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/spec", func(w http.ResponseWriter, r *http.Request) {
		s, err := spec.Load(root)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		v := view(s)
		// Tasks live in the database, not the spec; the UI gets them along.
		if proposals != nil {
			if v.Tasks, err = proposals.DB.Tasks(r.Context(), store.TaskFilter{}); err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "load tasks: " + err.Error()})
				return
			}
		}
		writeJSON(w, http.StatusOK, v)
	})
	mux.HandleFunc("GET /api/check", func(w http.ResponseWriter, r *http.Request) {
		report, err := check.Run(root, nil)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, report)
	})
	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, r *http.Request) {
		info := map[string]any{"editable": proposals != nil}
		if proposals != nil {
			info["base"] = proposals.Base
		} else if readonly != "" {
			info["readonlyReason"] = readonly
		}
		if repo != "" {
			info["repo"] = repo
		}
		if jc != nil {
			info["jira"] = map[string]string{"url": jc.URL, "project": jc.Project}
		}
		writeJSON(w, http.StatusOK, info)
	})
	mux.HandleFunc("GET /api/schemas", func(w http.ResponseWriter, r *http.Request) {
		out := map[string]json.RawMessage{}
		for name, data := range schema.All() {
			out[name] = data
		}
		writeJSON(w, http.StatusOK, out)
	})
	analyzer := &code.Analyzer{Root: root}
	mux.HandleFunc("GET /api/code/{component}", func(w http.ResponseWriter, r *http.Request) {
		m, err := analyzer.Analyze(r.PathValue("component"))
		switch {
		case errors.Is(err, code.ErrUnknownComponent):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, http.StatusOK, m)
		}
	})
	registerProposals(mux, root, proposals, newJiraSync(jc, proposals, logger))
	files := http.FileServerFS(static)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		// Built assets have content hashes in their names; index.html must
		// be revalidated so a rebuilt UI shows up on reload.
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
	return logRequests(logger, mux)
}

// specView is the JSON shape of /api/spec.
type specView struct {
	System       spec.System       `json:"system"`
	Tech         spec.Tech         `json:"tech"`
	Principles   spec.Principles   `json:"principles"`
	Domains      spec.Domains      `json:"domains"`
	Roadmap      spec.Roadmap      `json:"roadmap"`
	Requirements spec.Requirements `json:"requirements"`
	RuntimeRules []string          `json:"runtimeRules"`
	Risks        spec.Risks        `json:"risks"`
	Events       spec.Events       `json:"events"`
	Agents       []agentView       `json:"agents"`
	Decisions    []decisionView    `json:"decisions"`
	Tasks        []store.Task      `json:"tasks"`
}

type agentView struct {
	spec.Agent
	File string `json:"file"`
}

type decisionView struct {
	spec.Decision
	Body string `json:"body"`
	File string `json:"file"`
}

func view(s *spec.Spec) specView {
	v := specView{
		System:       s.System,
		Tech:         s.Tech,
		Principles:   s.Principles,
		Domains:      s.Domains,
		Roadmap:      s.Roadmap,
		Requirements: s.Requirements,
		RuntimeRules: check.RuntimeRules(),
		Risks:        s.Risks,
		Events:       s.Events,
		Agents:       []agentView{},
		Decisions:    []decisionView{},
		Tasks:        []store.Task{},
	}
	if v.Events.Events == nil {
		v.Events.Events = []spec.Event{}
	}
	if v.Risks.Risks == nil {
		v.Risks.Risks = []spec.Risk{}
	}
	if v.Requirements.Requirements == nil {
		v.Requirements.Requirements = []spec.Requirement{}
	}
	if v.Roadmap.Milestones == nil {
		v.Roadmap.Milestones = []spec.Milestone{}
	}
	for _, a := range s.Agents {
		v.Agents = append(v.Agents, agentView{Agent: a, File: a.File})
	}
	for _, d := range s.Decisions {
		v.Decisions = append(v.Decisions, decisionView{Decision: d, Body: d.Body, File: d.File})
	}
	return v
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		logger.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration", time.Since(start))
	})
}
