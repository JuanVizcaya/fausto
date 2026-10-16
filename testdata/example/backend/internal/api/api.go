// Package api is the HTTP/JSON gateway for the React frontend. Handlers
// translate requests into gRPC calls to domain services, so the browser API
// can evolve independently of internal contracts.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	residentsv1 "github.com/CMOT/habyta/backend/gen/habyta/residents/v1"
	"github.com/CMOT/habyta/backend/internal/platform/requestid"
	"github.com/CMOT/habyta/backend/internal/platform/server"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// callTimeout bounds each downstream call; it stays below the HTTP
// WriteTimeout so the gateway can still report the failure.
const callTimeout = 5 * time.Second

type gateway struct {
	logger    *slog.Logger
	residents residentsv1.ResidentServiceClient
}

// Register adds the gateway's HTTP routes to s.
func Register(s *server.Server, logger *slog.Logger, residents residentsv1.ResidentServiceClient) {
	g := &gateway{logger: logger, residents: residents}
	s.Handle("POST /v1/tenants/{tenantID}/residents", http.HandlerFunc(g.createResident))
	s.Handle("GET /v1/tenants/{tenantID}/residents", http.HandlerFunc(g.listResidents))
	s.Handle("GET /v1/tenants/{tenantID}/residents/{id}", http.HandlerFunc(g.getResident))
}

func callContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), callTimeout)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId,omitempty"`
}

func (g *gateway) writeError(w http.ResponseWriter, r *http.Request, code int, errCode, message string) {
	writeJSON(w, code, errorBody{Error: errorDetail{Code: errCode, Message: message, RequestID: requestid.From(r.Context())}})
}

// writeGRPCError maps a downstream gRPC error to an HTTP response. Messages
// for client errors are passed through; others are logged and hidden.
func (g *gateway) writeGRPCError(w http.ResponseWriter, r *http.Request, err error) {
	st := status.Convert(err)
	switch st.Code() {
	case codes.InvalidArgument, codes.FailedPrecondition, codes.OutOfRange:
		g.writeError(w, r, http.StatusBadRequest, "invalid_argument", st.Message())
	case codes.NotFound:
		g.writeError(w, r, http.StatusNotFound, "not_found", st.Message())
	case codes.AlreadyExists, codes.Aborted:
		g.writeError(w, r, http.StatusConflict, "conflict", st.Message())
	case codes.Unauthenticated:
		g.writeError(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
	case codes.PermissionDenied:
		g.writeError(w, r, http.StatusForbidden, "permission_denied", "permission denied")
	case codes.ResourceExhausted:
		g.writeError(w, r, http.StatusTooManyRequests, "rate_limited", "too many requests")
	case codes.Unavailable:
		g.logger.Warn("downstream unavailable", "request_id", requestid.From(r.Context()), "error", err)
		g.writeError(w, r, http.StatusServiceUnavailable, "unavailable", "service temporarily unavailable")
	case codes.DeadlineExceeded, codes.Canceled:
		g.logger.Warn("downstream timeout", "request_id", requestid.From(r.Context()), "error", err)
		g.writeError(w, r, http.StatusGatewayTimeout, "timeout", "upstream timeout")
	default:
		g.logger.Error("downstream error", "request_id", requestid.From(r.Context()), "error", err)
		g.writeError(w, r, http.StatusInternalServerError, "internal", "internal error")
	}
}

// decodeJSON reads a single JSON object and rejects unknown fields so typos
// in the frontend surface as 400s instead of silently ignored data.
func decodeJSON(r *http.Request, v any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			return errors.New("request body too large")
		}
		return errors.New("request body must be a valid JSON object with known fields")
	}
	if decoder.More() {
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}
