package administrators

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	administratorsv1 "github.com/CMOT/habyta/backend/gen/habyta/administrators/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

const (
	tenantA = "0b6e3b1e-6a59-4f7e-9d65-4d3a2f7c9a01"
	tenantB = "0b6e3b1e-6a59-4f7e-9d65-4d3a2f7c9a02"
)

func newClient(t *testing.T) administratorsv1.AdministratorServiceClient {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	clock := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	administratorsv1.RegisterAdministratorServiceServer(srv, newService(slog.New(slog.NewTextHandler(io.Discard, nil)), newStore(t), func() time.Time {
		clock = clock.Add(time.Second)
		return clock
	}))
	go srv.Serve(listener)
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return administratorsv1.NewAdministratorServiceClient(conn)
}

func create(t *testing.T, c administratorsv1.AdministratorServiceClient, tenant, email string) *administratorsv1.Administrator {
	t.Helper()
	resp, err := c.CreateAdministrator(context.Background(), &administratorsv1.CreateAdministratorRequest{
		TenantId: tenant, Company: "Administraciones Andinas", Email: email, Phone: "+57 300 123 4567",
	})
	if err != nil {
		t.Fatal(err)
	}
	return resp.GetAdministrator()
}

func TestCreateAndGet(t *testing.T) {
	c := newClient(t)
	created := create(t, c, tenantA, " Admin@Example.com ")
	if created.GetEmail() != "admin@example.com" || created.GetId() == "" || created.GetCreateTime() == nil {
		t.Fatalf("unexpected administrator: %v", created)
	}
	got, err := c.GetAdministrator(context.Background(), &administratorsv1.GetAdministratorRequest{TenantId: tenantA, Id: created.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetAdministrator().GetCompany() != "Administraciones Andinas" {
		t.Fatalf("got %v", got.GetAdministrator())
	}
}

func TestGetUnknown(t *testing.T) {
	c := newClient(t)
	_, err := c.GetAdministrator(context.Background(), &administratorsv1.GetAdministratorRequest{
		TenantId: tenantA, Id: "01a0f51b-0000-7000-8000-000000000001",
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %s, want NotFound", status.Code(err))
	}
}

func TestTenantIsolation(t *testing.T) {
	c := newClient(t)
	created := create(t, c, tenantA, "admin@example.com")
	_, err := c.GetAdministrator(context.Background(), &administratorsv1.GetAdministratorRequest{TenantId: tenantB, Id: created.GetId()})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("cross-tenant get: code = %s, want NotFound", status.Code(err))
	}
	list, err := c.ListAdministrators(context.Background(), &administratorsv1.ListAdministratorsRequest{TenantId: tenantB})
	if err != nil || len(list.GetAdministrators()) != 0 {
		t.Fatalf("cross-tenant list = %v, %v; want empty", list.GetAdministrators(), err)
	}
	// The same email may exist in another tenant.
	create(t, c, tenantB, "admin@example.com")
}

func TestDuplicateEmail(t *testing.T) {
	c := newClient(t)
	create(t, c, tenantA, "admin@example.com")
	_, err := c.CreateAdministrator(context.Background(), &administratorsv1.CreateAdministratorRequest{
		TenantId: tenantA, Company: "Other", Email: "ADMIN@example.com",
	})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("code = %s, want AlreadyExists", status.Code(err))
	}
}

func TestCreateValidation(t *testing.T) {
	c := newClient(t)
	valid := func() *administratorsv1.CreateAdministratorRequest {
		return &administratorsv1.CreateAdministratorRequest{TenantId: tenantA, Company: "Andinas", Email: "admin@example.com"}
	}
	for name, mutate := range map[string]func(*administratorsv1.CreateAdministratorRequest){
		"tenant":       func(r *administratorsv1.CreateAdministratorRequest) { r.TenantId = "" },
		"company":      func(r *administratorsv1.CreateAdministratorRequest) { r.Company = "  " },
		"email":        func(r *administratorsv1.CreateAdministratorRequest) { r.Email = "Admin <admin@example.com>" },
		"phone":        func(r *administratorsv1.CreateAdministratorRequest) { r.Phone = "call me" },
		"keycloak_sub": func(r *administratorsv1.CreateAdministratorRequest) { r.KeycloakSub = "not-a-uuid" },
	} {
		req := valid()
		mutate(req)
		if _, err := c.CreateAdministrator(context.Background(), req); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: code = %s, want InvalidArgument", name, status.Code(err))
		}
	}
}

func TestListPagination(t *testing.T) {
	c := newClient(t)
	var ids []string
	for _, email := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		ids = append(ids, create(t, c, tenantA, email).GetId())
	}
	create(t, c, tenantB, "d@example.com")

	var got []string
	token := ""
	for {
		resp, err := c.ListAdministrators(context.Background(), &administratorsv1.ListAdministratorsRequest{
			TenantId: tenantA, PageSize: 2, PageToken: token,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range resp.GetAdministrators() {
			got = append(got, a.GetId())
		}
		if token = resp.GetNextPageToken(); token == "" {
			break
		}
	}
	if len(got) != 3 || got[0] != ids[0] || got[1] != ids[1] || got[2] != ids[2] {
		t.Fatalf("paged IDs = %v, want %v", got, ids)
	}
	for name, req := range map[string]*administratorsv1.ListAdministratorsRequest{
		"bad token":     {TenantId: tenantA, PageToken: "!!"},
		"negative size": {TenantId: tenantA, PageSize: -1},
		"no tenant":     {},
	} {
		if _, err := c.ListAdministrators(context.Background(), req); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: code = %s, want InvalidArgument", name, status.Code(err))
		}
	}
}
