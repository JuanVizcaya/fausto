package residents

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	residentsv1 "github.com/CMOT/habyta/backend/gen/habyta/residents/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

const (
	tenantA = "0b6e3b1e-6a59-4f7e-9d65-4d3a2f7c9a01"
	tenantB = "0b6e3b1e-6a59-4f7e-9d65-4d3a2f7c9a02"
	unit1   = "5f0c1d2e-3b4a-4c5d-8e6f-7a8b9c0d1e01"
	unit2   = "5f0c1d2e-3b4a-4c5d-8e6f-7a8b9c0d1e02"
)

func newClient(t *testing.T) residentsv1.ResidentServiceClient {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	clock := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	residentsv1.RegisterResidentServiceServer(srv, newService(slog.New(slog.NewTextHandler(io.Discard, nil)), newStore(t), func() time.Time {
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
	return residentsv1.NewResidentServiceClient(conn)
}

func create(t *testing.T, c residentsv1.ResidentServiceClient, tenant, unit, email string) *residentsv1.Resident {
	t.Helper()
	resp, err := c.CreateResident(context.Background(), &residentsv1.CreateResidentRequest{
		TenantId: tenant, UnitId: unit, FullName: "Ana Pérez", Email: email, Phone: "+57 300 123 4567",
	})
	if err != nil {
		t.Fatal(err)
	}
	return resp.GetResident()
}

func TestCreateAndGet(t *testing.T) {
	c := newClient(t)
	created := create(t, c, tenantA, unit1, " Ana@Example.com ")
	if created.GetEmail() != "ana@example.com" || created.GetId() == "" || created.GetCreateTime() == nil {
		t.Fatalf("unexpected resident: %v", created)
	}
	got, err := c.GetResident(context.Background(), &residentsv1.GetResidentRequest{TenantId: tenantA, Id: created.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetResident().GetFullName() != "Ana Pérez" {
		t.Fatalf("got %v", got.GetResident())
	}
}

func TestTenantIsolation(t *testing.T) {
	c := newClient(t)
	created := create(t, c, tenantA, unit1, "ana@example.com")
	_, err := c.GetResident(context.Background(), &residentsv1.GetResidentRequest{TenantId: tenantB, Id: created.GetId()})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("cross-tenant get: code = %s, want NotFound", status.Code(err))
	}
	list, err := c.ListResidents(context.Background(), &residentsv1.ListResidentsRequest{TenantId: tenantB})
	if err != nil || len(list.GetResidents()) != 0 {
		t.Fatalf("cross-tenant list = %v, %v; want empty", list.GetResidents(), err)
	}
	// The same email may exist in another tenant.
	create(t, c, tenantB, unit1, "ana@example.com")
}

func TestDuplicateEmail(t *testing.T) {
	c := newClient(t)
	create(t, c, tenantA, unit1, "ana@example.com")
	_, err := c.CreateResident(context.Background(), &residentsv1.CreateResidentRequest{
		TenantId: tenantA, UnitId: unit2, FullName: "Other", Email: "ANA@example.com",
	})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("code = %s, want AlreadyExists", status.Code(err))
	}
}

func TestCreateValidation(t *testing.T) {
	c := newClient(t)
	valid := func() *residentsv1.CreateResidentRequest {
		return &residentsv1.CreateResidentRequest{TenantId: tenantA, UnitId: unit1, FullName: "Ana", Email: "ana@example.com"}
	}
	for name, mutate := range map[string]func(*residentsv1.CreateResidentRequest){
		"tenant":       func(r *residentsv1.CreateResidentRequest) { r.TenantId = "" },
		"unit":         func(r *residentsv1.CreateResidentRequest) { r.UnitId = "unit-1" },
		"name":         func(r *residentsv1.CreateResidentRequest) { r.FullName = "  " },
		"email":        func(r *residentsv1.CreateResidentRequest) { r.Email = "Ana <ana@example.com>" },
		"phone":        func(r *residentsv1.CreateResidentRequest) { r.Phone = "call me" },
		"keycloak_sub": func(r *residentsv1.CreateResidentRequest) { r.KeycloakSub = "not-a-uuid" },
	} {
		req := valid()
		mutate(req)
		if _, err := c.CreateResident(context.Background(), req); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: code = %s, want InvalidArgument", name, status.Code(err))
		}
	}
}

func TestListPaginationAndFilter(t *testing.T) {
	c := newClient(t)
	var ids []string
	for _, email := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		ids = append(ids, create(t, c, tenantA, unit1, email).GetId())
	}
	create(t, c, tenantA, unit2, "d@example.com")

	var got []string
	token := ""
	for {
		resp, err := c.ListResidents(context.Background(), &residentsv1.ListResidentsRequest{
			TenantId: tenantA, UnitId: unit1, PageSize: 2, PageToken: token,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range resp.GetResidents() {
			got = append(got, r.GetId())
		}
		if token = resp.GetNextPageToken(); token == "" {
			break
		}
	}
	if len(got) != 3 || got[0] != ids[0] || got[1] != ids[1] || got[2] != ids[2] {
		t.Fatalf("paged IDs = %v, want %v", got, ids)
	}
	_, err := c.ListResidents(context.Background(), &residentsv1.ListResidentsRequest{TenantId: tenantA, PageToken: "!!"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad token: code = %s, want InvalidArgument", status.Code(err))
	}
}
