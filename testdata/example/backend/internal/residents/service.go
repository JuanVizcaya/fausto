// Package residents implements the residents gRPC service.
package residents

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	residentsv1 "github.com/CMOT/habyta/backend/gen/habbyte/residents/v1"
	"github.com/CMOT/habyta/backend/internal/platform/postgres"
	"github.com/CMOT/habyta/backend/internal/platform/requestid"
	"github.com/CMOT/habyta/backend/internal/platform/server"
	"github.com/CMOT/habyta/backend/internal/platform/service"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultPageSize = 50
	maxPageSize     = 200
)

// Register connects to the residents database (DATABASE_URL), applies
// migrations, and adds the residents gRPC service and a database readiness
// check to s.
func Register(ctx context.Context, s *server.Server) error {
	url := service.Env("DATABASE_URL", "")
	if url == "" {
		return errors.New("DATABASE_URL is required")
	}
	pool, err := postgres.Open(ctx, s.Logger(), url)
	if err != nil {
		return err
	}
	s.OnShutdown(pool.Close)
	if err := postgres.Migrate(ctx, s.Logger(), pool, migrationFiles); err != nil {
		return err
	}
	s.AddReadinessCheck("postgres", pool.Ping)
	residentsv1.RegisterResidentServiceServer(s, newService(s.Logger(), &postgresStore{pool: pool}, time.Now))
	return nil
}

type residentService struct {
	residentsv1.UnimplementedResidentServiceServer
	logger *slog.Logger
	store  store
	now    func() time.Time
}

func newService(logger *slog.Logger, store store, now func() time.Time) *residentService {
	return &residentService{logger: logger, store: store, now: now}
}

func (s *residentService) CreateResident(ctx context.Context, req *residentsv1.CreateResidentRequest) (*residentsv1.CreateResidentResponse, error) {
	r, err := validateCreate(req)
	if err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, status.Error(codes.Internal, "generate ID")
	}
	r.ID = id.String()
	// Postgres stores microseconds; truncate so responses match stored rows.
	r.CreateTime = s.now().UTC().Truncate(time.Microsecond)
	r.UpdateTime = r.CreateTime
	if err := s.store.create(ctx, r); err != nil {
		if errors.Is(err, errEmailInUse) || errors.Is(err, errKeycloakSubInUse) {
			return nil, status.Error(codes.AlreadyExists, err.Error())
		}
		return nil, s.internalError(ctx, "create resident", err)
	}
	return &residentsv1.CreateResidentResponse{Resident: toProto(r)}, nil
}

func (s *residentService) GetResident(ctx context.Context, req *residentsv1.GetResidentRequest) (*residentsv1.GetResidentResponse, error) {
	if err := requireUUID("tenant_id", req.GetTenantId()); err != nil {
		return nil, err
	}
	if err := requireUUID("id", req.GetId()); err != nil {
		return nil, err
	}
	r, err := s.store.get(ctx, req.GetTenantId(), req.GetId())
	if errors.Is(err, errNotFound) {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	if err != nil {
		return nil, s.internalError(ctx, "get resident", err)
	}
	return &residentsv1.GetResidentResponse{Resident: toProto(r)}, nil
}

func (s *residentService) ListResidents(ctx context.Context, req *residentsv1.ListResidentsRequest) (*residentsv1.ListResidentsResponse, error) {
	if err := requireUUID("tenant_id", req.GetTenantId()); err != nil {
		return nil, err
	}
	if req.GetUnitId() != "" {
		if err := requireUUID("unit_id", req.GetUnitId()); err != nil {
			return nil, err
		}
	}
	size := int(req.GetPageSize())
	switch {
	case size < 0:
		return nil, status.Error(codes.InvalidArgument, "page_size must not be negative")
	case size == 0:
		size = defaultPageSize
	case size > maxPageSize:
		size = maxPageSize
	}
	after, err := decodePageToken(req.GetPageToken())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	// Fetch one extra row to learn whether another page exists.
	page, err := s.store.list(ctx, listQuery{TenantID: req.GetTenantId(), UnitID: req.GetUnitId(), After: after, Limit: size + 1})
	if err != nil {
		return nil, s.internalError(ctx, "list residents", err)
	}
	resp := &residentsv1.ListResidentsResponse{}
	if len(page) > size {
		page = page[:size]
		last := page[size-1]
		resp.NextPageToken = encodePageToken(cursor{CreateTime: last.CreateTime, ID: last.ID})
	}
	resp.Residents = make([]*residentsv1.Resident, 0, len(page))
	for _, r := range page {
		resp.Residents = append(resp.Residents, toProto(r))
	}
	return resp, nil
}

func validateCreate(req *residentsv1.CreateResidentRequest) (resident, error) {
	r := resident{
		TenantID:    req.GetTenantId(),
		UnitID:      req.GetUnitId(),
		FullName:    strings.TrimSpace(req.GetFullName()),
		Email:       strings.ToLower(strings.TrimSpace(req.GetEmail())),
		Phone:       strings.TrimSpace(req.GetPhone()),
		KeycloakSub: req.GetKeycloakSub(),
	}
	if err := requireUUID("tenant_id", r.TenantID); err != nil {
		return r, err
	}
	if err := requireUUID("unit_id", r.UnitID); err != nil {
		return r, err
	}
	if r.FullName == "" || utf8.RuneCountInString(r.FullName) > 200 {
		return r, status.Error(codes.InvalidArgument, "full_name is required and must be at most 200 characters")
	}
	if address, err := mail.ParseAddress(r.Email); err != nil || address.Address != r.Email || len(r.Email) > 254 {
		return r, status.Error(codes.InvalidArgument, "email must be a valid address")
	}
	if !validPhone(r.Phone) {
		return r, status.Error(codes.InvalidArgument, "phone may contain only digits, spaces, and + - ( ), up to 32 characters")
	}
	if r.KeycloakSub != "" {
		if err := requireUUID("keycloak_sub", r.KeycloakSub); err != nil {
			return r, err
		}
	}
	return r, nil
}

func requireUUID(field, value string) error {
	if _, err := uuid.Parse(value); err != nil || len(value) != 36 {
		return status.Errorf(codes.InvalidArgument, "%s must be a UUID", field)
	}
	return nil
}

func validPhone(phone string) bool {
	if len(phone) > 32 {
		return false
	}
	for _, c := range phone {
		if !(c >= '0' && c <= '9' || strings.ContainsRune(" +-()", c)) {
			return false
		}
	}
	return true
}

// Page tokens are opaque to clients: base64 of "<unix microseconds>.<id>"
// for the last row of the previous page.
func encodePageToken(c cursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(c.CreateTime.UnixMicro(), 10) + "." + c.ID))
}

func decodePageToken(token string) (*cursor, error) {
	if token == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, errInvalidPageToken
	}
	micros, id, ok := strings.Cut(string(b), ".")
	if !ok {
		return nil, errInvalidPageToken
	}
	n, err := strconv.ParseInt(micros, 10, 64)
	if err != nil || requireUUID("page_token", id) != nil {
		return nil, errInvalidPageToken
	}
	return &cursor{CreateTime: time.UnixMicro(n).UTC(), ID: id}, nil
}

// internalError logs err with the request ID and returns a gRPC error that
// does not expose internal details: Unavailable when the database cannot be
// reached (retryable), otherwise Internal.
func (s *residentService) internalError(ctx context.Context, op string, err error) error {
	if postgres.IsUnavailable(err) {
		s.logger.WarnContext(ctx, op+" failed: database unavailable", "request_id", requestid.From(ctx), "error", err)
		return status.Error(codes.Unavailable, "database unavailable")
	}
	s.logger.ErrorContext(ctx, op+" failed", "request_id", requestid.From(ctx), "error", err)
	return status.Error(codes.Internal, fmt.Sprintf("%s failed", op))
}

func toProto(r resident) *residentsv1.Resident {
	return &residentsv1.Resident{
		Id:          r.ID,
		TenantId:    r.TenantID,
		UnitId:      r.UnitID,
		FullName:    r.FullName,
		Email:       r.Email,
		Phone:       r.Phone,
		KeycloakSub: r.KeycloakSub,
		CreateTime:  timestamppb.New(r.CreateTime),
		UpdateTime:  timestamppb.New(r.UpdateTime),
	}
}
