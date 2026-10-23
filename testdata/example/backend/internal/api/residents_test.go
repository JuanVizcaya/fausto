package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	residentsv1 "github.com/CMOT/habyta/backend/gen/habbyte/residents/v1"
	"github.com/CMOT/habyta/backend/internal/platform/requestid"
	"github.com/CMOT/habyta/backend/internal/platform/server"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const tenant = "0b6e3b1e-6a59-4f7e-9d65-4d3a2f7c9a01"

// fakeResidents records requests and returns canned results.
type fakeResidents struct {
	residentsv1.ResidentServiceClient
	createReq *residentsv1.CreateResidentRequest
	listReq   *residentsv1.ListResidentsRequest
	requestID string
	err       error
}

func (f *fakeResidents) CreateResident(ctx context.Context, req *residentsv1.CreateResidentRequest, _ ...grpc.CallOption) (*residentsv1.CreateResidentResponse, error) {
	f.createReq = req
	f.requestID = requestid.From(ctx)
	if f.err != nil {
		return nil, f.err
	}
	return &residentsv1.CreateResidentResponse{Resident: sampleResident(req.GetTenantId())}, nil
}

func (f *fakeResidents) GetResident(ctx context.Context, req *residentsv1.GetResidentRequest, _ ...grpc.CallOption) (*residentsv1.GetResidentResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &residentsv1.GetResidentResponse{Resident: sampleResident(req.GetTenantId())}, nil
}

func (f *fakeResidents) ListResidents(ctx context.Context, req *residentsv1.ListResidentsRequest, _ ...grpc.CallOption) (*residentsv1.ListResidentsResponse, error) {
	f.listReq = req
	if f.err != nil {
		return nil, f.err
	}
	return &residentsv1.ListResidentsResponse{NextPageToken: "next"}, nil
}

func sampleResident(tenantID string) *residentsv1.Resident {
	ts := timestamppb.New(time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
	return &residentsv1.Resident{Id: "r1", TenantId: tenantID, UnitId: "u1", FullName: "Ana", Email: "ana@example.com", CreateTime: ts, UpdateTime: ts}
}

func newHandler(f *fakeResidents) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := server.New(logger, time.Second)
	Register(s, logger, f)
	return s.Handler()
}

func do(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set(requestid.Header, "req-1")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	return response
}

func TestCreateResident(t *testing.T) {
	f := &fakeResidents{}
	response := do(newHandler(f), http.MethodPost, "/v1/tenants/"+tenant+"/residents",
		`{"unitId":"u1","fullName":"Ana","email":"ana@example.com"}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", response.Code, response.Body)
	}
	if f.createReq.GetTenantId() != tenant || f.createReq.GetFullName() != "Ana" {
		t.Fatalf("gRPC request = %v", f.createReq)
	}
	if f.requestID != "req-1" {
		t.Fatalf("request ID not propagated: %q", f.requestID)
	}
	if got := response.Header().Get("Location"); got != "/v1/tenants/"+tenant+"/residents/r1" {
		t.Fatalf("Location = %q", got)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["fullName"] != "Ana" || body["createTime"] != "2026-09-01T12:00:00Z" {
		t.Fatalf("body = %v", body)
	}
}

func TestCreateResidentRejectsBadJSON(t *testing.T) {
	f := &fakeResidents{}
	h := newHandler(f)
	for _, body := range []string{`{"unknown":1}`, `not json`, `{} {}`} {
		response := do(h, http.MethodPost, "/v1/tenants/"+tenant+"/residents", body)
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", body, response.Code)
		}
	}
	if f.createReq != nil {
		t.Fatal("invalid JSON reached the residents service")
	}
}

func TestListResidentsQuery(t *testing.T) {
	f := &fakeResidents{}
	h := newHandler(f)
	response := do(h, http.MethodGet, "/v1/tenants/"+tenant+"/residents?unitId=u1&pageSize=10&pageToken=abc", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if f.listReq.GetUnitId() != "u1" || f.listReq.GetPageSize() != 10 || f.listReq.GetPageToken() != "abc" {
		t.Fatalf("gRPC request = %v", f.listReq)
	}
	if !strings.Contains(response.Body.String(), `"residents":[]`) {
		t.Fatalf("empty list should encode as [], got %s", response.Body)
	}
	if response := do(h, http.MethodGet, "/v1/tenants/"+tenant+"/residents?pageSize=ten", ""); response.Code != http.StatusBadRequest {
		t.Fatalf("bad pageSize: status = %d, want 400", response.Code)
	}
}

func TestGRPCErrorMapping(t *testing.T) {
	for code, want := range map[codes.Code]int{
		codes.InvalidArgument:  http.StatusBadRequest,
		codes.NotFound:         http.StatusNotFound,
		codes.AlreadyExists:    http.StatusConflict,
		codes.PermissionDenied: http.StatusForbidden,
		codes.Unauthenticated:  http.StatusUnauthorized,
		codes.Unavailable:      http.StatusServiceUnavailable,
		codes.DeadlineExceeded: http.StatusGatewayTimeout,
		codes.Internal:         http.StatusInternalServerError,
	} {
		f := &fakeResidents{err: status.Error(code, "secret detail")}
		response := do(newHandler(f), http.MethodGet, "/v1/tenants/"+tenant+"/residents/r1", "")
		if response.Code != want {
			t.Errorf("%s: status = %d, want %d", code, response.Code, want)
		}
		var body errorBody
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: %v", code, err)
		}
		if body.Error.RequestID != "req-1" {
			t.Errorf("%s: requestId = %q", code, body.Error.RequestID)
		}
		if want >= 500 && strings.Contains(body.Error.Message, "secret") {
			t.Errorf("%s: internal message leaked: %q", code, body.Error.Message)
		}
	}
}
