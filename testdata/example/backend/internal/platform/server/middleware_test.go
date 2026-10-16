package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CMOT/habyta/backend/internal/platform/requestid"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestMiddlewareRecoversPanic(t *testing.T) {
	h := withMiddleware(discardLogger(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
}

func TestMiddlewareRequestID(t *testing.T) {
	h := withMiddleware(discardLogger(), http.NotFoundHandler())
	for _, tc := range []struct {
		incoming string
		keep     bool
	}{
		{"abc-123", true},
		{"", false},
		{"bad id\n", false},
		{strings.Repeat("a", 65), false},
	} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set(requestid.Header, tc.incoming)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		got := response.Header().Get(requestid.Header)
		if tc.keep && got != tc.incoming {
			t.Errorf("request ID %q replaced with %q", tc.incoming, got)
		}
		if !tc.keep && (got == tc.incoming || !requestid.Valid(got)) {
			t.Errorf("request ID %q not replaced with a valid one: %q", tc.incoming, got)
		}
	}
}

func TestMiddlewareLimitsBody(t *testing.T) {
	h := withMiddleware(discardLogger(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		}
	}))
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", maxBodyBytes+1))))
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", response.Code)
	}
}

func TestUnaryRecovery(t *testing.T) {
	_, err := unaryRecovery(discardLogger())(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/test/Panic"},
		func(context.Context, any) (any, error) { panic("boom") })
	if status.Code(err) != codes.Internal {
		t.Fatalf("code = %s, want Internal", status.Code(err))
	}
}

func TestMiddlewareRequestIDInContext(t *testing.T) {
	var got string
	h := withMiddleware(discardLogger(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = requestid.From(r.Context())
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(requestid.Header, "abc-123")
	h.ServeHTTP(httptest.NewRecorder(), request)
	if got != "abc-123" {
		t.Fatalf("request ID in context = %q, want abc-123", got)
	}
}

func TestUnaryLoggingRequestID(t *testing.T) {
	interceptor := unaryLogging(discardLogger())
	info := &grpc.UnaryServerInfo{FullMethod: "/test/Echo"}
	var got string
	handler := func(ctx context.Context, _ any) (any, error) {
		got = requestid.From(ctx)
		return nil, nil
	}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(requestid.MetadataKey, "abc-123"))
	_, _ = interceptor(ctx, nil, info, handler)
	if got != "abc-123" {
		t.Fatalf("propagated request ID = %q, want abc-123", got)
	}
	_, _ = interceptor(context.Background(), nil, info, handler)
	if !requestid.Valid(got) || got == "abc-123" {
		t.Fatalf("request ID without metadata = %q, want a new valid ID", got)
	}
}
