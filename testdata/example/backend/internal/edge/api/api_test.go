package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Ceferinto/habbyte/backend/internal/platform/httpserver"
	"github.com/Ceferinto/habbyte/backend/internal/platform/requestid"
)

func TestUnknownRouteUsesErrorFormat(t *testing.T) {
	s := httpserver.New(slog.New(slog.NewTextHandler(io.Discard, nil)), 5*time.Second)
	Register(s)
	request := httptest.NewRequest(http.MethodGet, "/v1/nothing", nil)
	request.Header.Set(requestid.Header, "abc-123")
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
	var body errorBody
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "not_found" || body.Error.RequestID != "abc-123" {
		t.Fatalf("error body = %+v", body.Error)
	}
}
