package httpserver

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/Ceferinto/habbyte/backend/internal/platform/requestid"
)

const (
	// maxBodyBytes bounds request bodies by default; upload handlers can
	// replace r.Body with a larger http.MaxBytesReader.
	maxBodyBytes = 1 << 20
)

// withMiddleware wraps h with panic recovery, request IDs, body limits, and
// access logging. Probe endpoints log at debug level to keep logs readable.
func withMiddleware(logger *slog.Logger, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := r.Header.Get(requestid.Header)
		if !requestid.Valid(id) {
			id = requestid.New()
		}
		w.Header().Set(requestid.Header, id)
		r = r.WithContext(requestid.With(r.Context(), id))
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				logger.Error("http handler panic", "request_id", id, "panic", v, "stack", string(debug.Stack()))
				if !rec.wroteHeader {
					http.Error(rec, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
			}
			level := slog.LevelInfo
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
				level = slog.LevelDebug
			}
			logger.Log(r.Context(), level, "http request",
				"request_id", id,
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		}()
		h.ServeHTTP(rec, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wroteHeader = true
	return r.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
