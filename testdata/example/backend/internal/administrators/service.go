// Package administrators implements the administrators gRPC service.
package administrators

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

	administratorsv1 "github.com/CMOT/habyta/backend/gen/habyta/administrators/v1"
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

// Register connects to the administrators database (DATABASE_URL), applies
// migrations, and adds the administrators gRPC service and a database
// readiness check to s.
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
	administratorsv1.RegisterAdministratorServiceServer(s, newService(s.Logger(), &postgresStore{pool: pool}, time.Now))
	return nil
}

type administratorService struct {
	administratorsv1.UnimplementedAdministratorServiceServer
	logger *slog.Logger
	store  store
	now    func() time.Time
}

func newService(logger *slog.Logger, store store, now func() time.Time) *administratorService {
	return &administratorService{logger: logger, store: store, now: now}
}

func (s *administratorService) CreateAdministrator(ctx context.Context, req *administratorsv1.CreateAdministratorRequest) (*administratorsv1.CreateAdministratorResponse, error) {
	a, err := validateCreate(req)
	if err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, status.Error(codes.Internal, "generate ID")
	}
	a.ID = id.String()
	// Postgres stores microseconds; truncate so responses match stored rows.
	a.CreateTime = s.now().UTC().Truncate(time.Microsecond)
	a.UpdateTime = a.CreateTime
	if err := s.store.create(ctx, a); err != nil {
		if errors.Is(err, errEmailInUse) || errors.Is(err, errKeycloakSubInUse) {
			return nil, status.Error(codes.AlreadyExists, err.Error())
		}
		return nil, s.internalError(ctx, "create administrator", err)
	}
	return &administratorsv1.CreateAdministratorResponse{Administrator: toProto(a)}, nil
}

func (s *administratorService) GetAdministrator(ctx context.Context, req *administratorsv1.GetAdministratorRequest) (*administratorsv1.GetAdministratorResponse, error) {
	if err := requireUUID("tenant_id", req.GetTenantId()); err != nil {
		return nil, err
	}
	if err := requireUUID("id", req.GetId()); err != nil {
		return nil, err
	}
	a, err := s.store.get(ctx, req.GetTenantId(), req.GetId())
	if errors.Is(err, errNotFound) {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	if err != nil {
		return nil, s.internalError(ctx, "get administrator", err)
	}
	return &administratorsv1.GetAdministratorResponse{Administrator: toProto(a)}, nil
}

func (s *administratorService) ListAdministrators(ctx context.Context, req *administratorsv1.ListAdministratorsRequest) (*administratorsv1.ListAdministratorsResponse, error) {
	if err := requireUUID("tenant_id", req.GetTenantId()); err != nil {
		return nil, err
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
	page, err := s.store.list(ctx, listQuery{TenantID: req.GetTenantId(), After: after, Limit: size + 1})
	if err != nil {
		return nil, s.internalError(ctx, "list administrators", err)
	}
	resp := &administratorsv1.ListAdministratorsResponse{}
	if len(page) > size {
		page = page[:size]
		last := page[size-1]
		resp.NextPageToken = encodePageToken(cursor{CreateTime: last.CreateTime, ID: last.ID})
	}
	resp.Administrators = make([]*administratorsv1.Administrator, 0, len(page))
	for _, a := range page {
		resp.Administrators = append(resp.Administrators, toProto(a))
	}
	return resp, nil
}

func validateCreate(req *administratorsv1.CreateAdministratorRequest) (administrator, error) {
	a := administrator{
		TenantID:    req.GetTenantId(),
		Company:     strings.TrimSpace(req.GetCompany()),
		Email:       strings.ToLower(strings.TrimSpace(req.GetEmail())),
		Phone:       strings.TrimSpace(req.GetPhone()),
		KeycloakSub: req.GetKeycloakSub(),
	}
	if err := requireUUID("tenant_id", a.TenantID); err != nil {
		return a, err
	}
	if a.Company == "" || utf8.RuneCountInString(a.Company) > 200 {
		return a, status.Error(codes.InvalidArgument, "company is required and must be at most 200 characters")
	}
	if address, err := mail.ParseAddress(a.Email); err != nil || address.Address != a.Email || len(a.Email) > 254 {
		return a, status.Error(codes.InvalidArgument, "email must be a valid address")
	}
	if !validPhone(a.Phone) {
		return a, status.Error(codes.InvalidArgument, "phone may contain only digits, spaces, and + - ( ), up to 32 characters")
	}
	if a.KeycloakSub != "" {
		if err := requireUUID("keycloak_sub", a.KeycloakSub); err != nil {
			return a, err
		}
	}
	return a, nil
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
func (s *administratorService) internalError(ctx context.Context, op string, err error) error {
	if postgres.IsUnavailable(err) {
		s.logger.WarnContext(ctx, op+" failed: database unavailable", "request_id", requestid.From(ctx), "error", err)
		return status.Error(codes.Unavailable, "database unavailable")
	}
	s.logger.ErrorContext(ctx, op+" failed", "request_id", requestid.From(ctx), "error", err)
	return status.Error(codes.Internal, fmt.Sprintf("%s failed", op))
}

func toProto(a administrator) *administratorsv1.Administrator {
	return &administratorsv1.Administrator{
		Id:          a.ID,
		TenantId:    a.TenantID,
		Company:     a.Company,
		Email:       a.Email,
		Phone:       a.Phone,
		KeycloakSub: a.KeycloakSub,
		CreateTime:  timestamppb.New(a.CreateTime),
		UpdateTime:  timestamppb.New(a.UpdateTime),
	}
}
