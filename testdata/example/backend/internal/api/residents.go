package api

import (
	"net/http"
	"strconv"
	"time"

	residentsv1 "github.com/CMOT/habyta/backend/gen/habyta/residents/v1"
)

// residentJSON is the browser representation of a resident.
type residentJSON struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenantId"`
	UnitID      string    `json:"unitId"`
	FullName    string    `json:"fullName"`
	Email       string    `json:"email"`
	Phone       string    `json:"phone,omitempty"`
	KeycloakSub string    `json:"keycloakSub,omitempty"`
	CreateTime  time.Time `json:"createTime"`
	UpdateTime  time.Time `json:"updateTime"`
}

type createResidentJSON struct {
	UnitID      string `json:"unitId"`
	FullName    string `json:"fullName"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	KeycloakSub string `json:"keycloakSub"`
}

type listResidentsJSON struct {
	Residents     []residentJSON `json:"residents"`
	NextPageToken string         `json:"nextPageToken,omitempty"`
}

func fromProto(r *residentsv1.Resident) residentJSON {
	return residentJSON{
		ID:          r.GetId(),
		TenantID:    r.GetTenantId(),
		UnitID:      r.GetUnitId(),
		FullName:    r.GetFullName(),
		Email:       r.GetEmail(),
		Phone:       r.GetPhone(),
		KeycloakSub: r.GetKeycloakSub(),
		CreateTime:  r.GetCreateTime().AsTime(),
		UpdateTime:  r.GetUpdateTime().AsTime(),
	}
}

func (g *gateway) createResident(w http.ResponseWriter, r *http.Request) {
	var body createResidentJSON
	if err := decodeJSON(r, &body); err != nil {
		g.writeError(w, r, http.StatusBadRequest, "invalid_argument", err.Error())
		return
	}
	ctx, cancel := callContext(r)
	defer cancel()
	resp, err := g.residents.CreateResident(ctx, &residentsv1.CreateResidentRequest{
		TenantId:    r.PathValue("tenantID"),
		UnitId:      body.UnitID,
		FullName:    body.FullName,
		Email:       body.Email,
		Phone:       body.Phone,
		KeycloakSub: body.KeycloakSub,
	})
	if err != nil {
		g.writeGRPCError(w, r, err)
		return
	}
	created := fromProto(resp.GetResident())
	w.Header().Set("Location", r.URL.Path+"/"+created.ID)
	writeJSON(w, http.StatusCreated, created)
}

func (g *gateway) getResident(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := callContext(r)
	defer cancel()
	resp, err := g.residents.GetResident(ctx, &residentsv1.GetResidentRequest{
		TenantId: r.PathValue("tenantID"),
		Id:       r.PathValue("id"),
	})
	if err != nil {
		g.writeGRPCError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, fromProto(resp.GetResident()))
}

func (g *gateway) listResidents(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var pageSize int32
	if raw := query.Get("pageSize"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			g.writeError(w, r, http.StatusBadRequest, "invalid_argument", "pageSize must be an integer")
			return
		}
		pageSize = int32(n)
	}
	ctx, cancel := callContext(r)
	defer cancel()
	resp, err := g.residents.ListResidents(ctx, &residentsv1.ListResidentsRequest{
		TenantId:  r.PathValue("tenantID"),
		UnitId:    query.Get("unitId"),
		PageSize:  pageSize,
		PageToken: query.Get("pageToken"),
	})
	if err != nil {
		g.writeGRPCError(w, r, err)
		return
	}
	out := listResidentsJSON{Residents: make([]residentJSON, 0, len(resp.GetResidents())), NextPageToken: resp.GetNextPageToken()}
	for _, resident := range resp.GetResidents() {
		out.Residents = append(out.Residents, fromProto(resident))
	}
	writeJSON(w, http.StatusOK, out)
}
