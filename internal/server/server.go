// Package server serves the fausto web UI and a JSON API over the system
// spec, the drift checks, and the code of each component. Every request
// rereads system/, so edits show up on reload.
package server

import (
	"context"
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
	"github.com/JuanVizcaya/fausto/internal/prcheck"
	"github.com/JuanVizcaya/fausto/internal/proposal"
	"github.com/JuanVizcaya/fausto/internal/schema"
	"github.com/JuanVizcaya/fausto/internal/source"
	"github.com/JuanVizcaya/fausto/internal/spec"
	"github.com/JuanVizcaya/fausto/internal/store"
)

// dist is the built web UI (fausto/web, `npm run build`).
//
//go:embed dist
var dist embed.FS

// Options configures the handler for one project.
type Options struct {
	// Project is the project's id; agents' tokens are for one project.
	Project string
	// Tokens finds agents' tokens; nil turns the agent API off.
	Tokens Tokens
	// Root is the project's work tree.
	Root string
	// Proposals may be nil, which disables editing (for example without a
	// database); Readonly then says why and how to enable it, for the UI
	// to show.
	Proposals *proposal.Store
	Readonly  string
	// Repo is the repository's GitHub URL (e.g.
	// "https://github.com/owner/repo"), for the UI to link into; empty
	// when it couldn't be resolved.
	Repo string
	// Jira, when set, mirrors tasks as Jira issues.
	Jira   *jira.Client
	Logger *slog.Logger
	// Poll, when positive, is how often accepted proposals are checked for
	// a merge, and pull requests checked (PRCheck), in the background, for
	// as long as ctx lasts (see Start).
	Poll time.Duration
	// PRCheck, when set, checks the project's open pull requests against
	// the spec fausto keeps and reports on GitHub; PRCheckOff otherwise
	// says why it is off, for the UI.
	PRCheck    *prcheck.Checker
	PRCheckOff string
}

// Handler serves one project.
type Handler struct {
	http.Handler
	sync *syncer
}

// New returns a handler for the project o describes.
func New(o Options) *Handler {
	root, proposals, readonly, repo, jc, logger := o.Root, o.Proposals, o.Readonly, o.Repo, o.Jira, o.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	static, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is embedded at build time
	}
	mux := http.NewServeMux()
	// The spec as fausto sees it: with the charters it keeps, if it does.
	specOptions := func(r *http.Request) (spec.Options, error) {
		if proposals == nil {
			return spec.Options{}, nil
		}
		return proposals.SpecOptions(r.Context())
	}
	mux.HandleFunc("GET /api/spec", func(w http.ResponseWriter, r *http.Request) {
		opts, err := specOptions(r)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "load charters: " + err.Error()})
			return
		}
		s, err := spec.LoadWith(root, opts)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		v := view(s)
		v.Charters, v.Source = s.Charters, s.Source
		if proposals != nil {
			if rev, err := proposals.DB.SpecRevision(r.Context(), 0); err == nil {
				v.Revision = rev.N
			}
		}
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
		opts, err := specOptions(r)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "load charters: " + err.Error()})
			return
		}
		report, err := check.RunWith(root, nil, opts)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, report)
	})
	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, r *http.Request) {
		info := map[string]any{"editable": proposals != nil}
		if proposals != nil {
			info["base"] = proposals.Base()
			info["source"] = "local"
			if _, ok := proposals.Src.(*source.GitHub); ok {
				info["source"] = "github"
			}
		} else if readonly != "" {
			info["readonlyReason"] = readonly
		}
		if repo != "" {
			info["repo"] = repo
		}
		if o.PRCheck != nil {
			info["prChecks"] = o.PRCheck.Repo
		} else if o.PRCheckOff != "" {
			info["prChecksOff"] = o.PRCheckOff
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
	if proposals != nil {
		analyzer.Spec = func() (spec.Options, error) { return proposals.SpecOptions(context.Background()) }
	}
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
	js := newJiraSync(jc, proposals, logger)
	sy := newSyncer(proposals, js, logger, o.Poll)
	if sy != nil {
		sy.prs = o.PRCheck
	}
	registerProposals(mux, root, proposals, js, sy)
	registerAgentAPI(mux, o.Project, root, proposals, o.Tokens, js)
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
	return &Handler{Handler: logRequests(logger, mux), sync: sy}
}

// Start polls accepted proposals for a merge until ctx is done, when
// Options.Poll is set.
func (h *Handler) Start(ctx context.Context) {
	if h.sync != nil && h.sync.every > 0 {
		go h.sync.loop(ctx)
	}
}

// specView is the JSON shape of /api/spec.
type specView struct {
	System       spec.System       `json:"system"`
	Tech         spec.Tech         `json:"tech"`
	Principles   spec.Principles   `json:"principles"`
	Checks       spec.Checks       `json:"checks"`
	Domains      spec.Domains      `json:"domains"`
	Roadmap      spec.Roadmap      `json:"roadmap"`
	Requirements spec.Requirements `json:"requirements"`
	RuntimeRules []string          `json:"runtimeRules"`
	Risks        spec.Risks        `json:"risks"`
	Events       spec.Events       `json:"events"`
	Agents       []agentView       `json:"agents"`
	Decisions    []decisionView    `json:"decisions"`
	Tasks        []store.Task      `json:"tasks"`
	// Charters is where the agent charters are kept: "repo", "fausto", or
	// "" when there are none. Source is where the spec is kept, and
	// Revision its revision when fausto keeps it.
	Charters string `json:"charters"`
	Source   string `json:"source"`
	Revision int    `json:"revision,omitempty"`
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
		Checks:       s.Checks,
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
