// Package api is Habbyte's public HTTP/JSON API under /v1. It holds user
// sessions, checks permissions through iam, and composes screens from the
// modules' contracts. Clients never see those contracts.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/Ceferinto/habbyte/backend/internal/platform/httpserver"
	"github.com/Ceferinto/habbyte/backend/internal/platform/requestid"
)

// Register adds the /v1 routes to s. Routes not defined yet answer 404 in
// the API's error format.
func Register(s *httpserver.Server) {
	logger := s.Logger()
	s.Handle("/v1/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, logger, http.StatusNotFound, "not_found", "No such resource.")
	}))
}

// errorBody is the API's error format; code is stable and the frontend
// translates it.
type errorBody struct {
	Error struct {
		Code      string         `json:"code"`
		Message   string         `json:"message"`
		RequestID string         `json:"requestId"`
		Details   map[string]any `json:"details,omitempty"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, status int, code, message string) {
	var body errorBody
	body.Error.Code = code
	body.Error.Message = message
	body.Error.RequestID = requestid.From(r.Context())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logger.Warn("write error response", "error", err)
	}
}
