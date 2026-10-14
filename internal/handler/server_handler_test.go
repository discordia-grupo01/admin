package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/apierr"
	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/service"
)

type fakeServerLister struct {
	list   *model.ServerList
	err    error
	got    service.ListServersParams
	called bool
}

func (f *fakeServerLister) List(_ context.Context, p service.ListServersParams) (*model.ServerList, error) {
	f.called = true
	f.got = p
	return f.list, f.err
}

func serversRouter(l ServerLister) http.Handler {
	return NewRouter(Deps{JWTSecret: []byte(secret), Staff: fakeStaff{}, Health: NewHealthHandler()},
		NewServerHandler(l).Register)
}

func TestListServersReturnsPage(t *testing.T) {
	ownerName := "Manu"
	lister := &fakeServerLister{list: &model.ServerList{
		Servers: []model.Server{
			{ID: "s1", Name: "Gamers", OwnerID: "u1", OwnerName: &ownerName, MemberCount: 12, CreatedAt: time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)},
			{ID: "s2", Name: "Sin dueño replicado", OwnerID: "u2", MemberCount: 0, CreatedAt: time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)},
		},
		Total: 2, Limit: 20, Offset: 0,
	}}
	w := get(serversRouter(lister), "/v1/admin/servers", bearer(t, staffID))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Servers []map[string]any `json:"servers"`
		Total   int              `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 2 || len(body.Servers) != 2 {
		t.Fatalf("body: %s", w.Body.String())
	}
	first, second := body.Servers[0], body.Servers[1]
	if first["name"] != "Gamers" || first["owner_name"] != "Manu" || first["member_count"] != float64(12) || first["owner_id"] != "u1" {
		t.Errorf("servidor: %v", first)
	}
	if first["created_at"] != "2026-09-01T10:00:00Z" {
		t.Errorf("created_at debe ser ISO 8601 en UTC: %v", first["created_at"])
	}
	if v, present := second["owner_name"]; !present || v != nil {
		t.Errorf("sin el dueño replicado owner_name debe ser null (presente), no %v", v)
	}
}

func TestListServersPassesQueryParameters(t *testing.T) {
	lister := &fakeServerLister{list: &model.ServerList{Servers: []model.Server{}}}
	w := get(serversRouter(lister), "/v1/admin/servers?search=%20gamers%20&limit=10&offset=30", bearer(t, staffID))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if want := (service.ListServersParams{Search: "gamers", Limit: 10, Offset: 30}); lister.got != want {
		t.Errorf("params = %+v, quería %+v", lister.got, want)
	}
}

func TestListServersDefaultsWhenNoParameters(t *testing.T) {
	lister := &fakeServerLister{list: &model.ServerList{Servers: []model.Server{}}}
	get(serversRouter(lister), "/v1/admin/servers", bearer(t, staffID))
	if lister.got.Limit != model.DefaultLimit || lister.got.Offset != 0 || lister.got.Search != "" {
		t.Errorf("params por defecto = %+v", lister.got)
	}
}

func TestListServersEmptyResultIsAnEmptyList(t *testing.T) {
	lister := &fakeServerLister{list: &model.ServerList{Servers: []model.Server{}, Limit: 20}}
	w := get(serversRouter(lister), "/v1/admin/servers?search=nadie", bearer(t, staffID))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"servers":[]`) {
		t.Fatalf("un resultado vacío es 200 con servers:[], no null ni 404: %d %s", w.Code, w.Body.String())
	}
}

func TestListServersInvalidParameters(t *testing.T) {
	cases := map[string]string{
		"limit negativo":    "?limit=-1",
		"limit no numérico": "?limit=muchos",
		"offset negativo":   "?offset=-5",
		"search muy largo":  "?search=" + strings.Repeat("a", 101),
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			lister := &fakeServerLister{}
			w := get(serversRouter(lister), "/v1/admin/servers"+query, bearer(t, staffID))
			var body apierr.ErrorResponse
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			if w.Code != http.StatusBadRequest || body.Error.Code != apierr.CodeInvalidQuery {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if lister.called {
				t.Error("un parámetro inválido no debe llegar al servicio")
			}
		})
	}
}

func TestListServersServiceErrorIsInternal(t *testing.T) {
	w := get(serversRouter(&fakeServerLister{err: errors.New("db caída")}), "/v1/admin/servers", bearer(t, staffID))
	var body apierr.ErrorResponse
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != http.StatusInternalServerError || body.Error.Code != apierr.CodeInternal {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "db caída") {
		t.Error("el detalle del error interno no debe llegar al cliente")
	}
}

func TestListServersRequiresStaff(t *testing.T) {
	cases := []struct {
		name, auth string
		status     int
	}{
		{"sin token", "", http.StatusUnauthorized},
		{"no es staff", bearer(t, userID), http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lister := &fakeServerLister{list: &model.ServerList{Servers: []model.Server{}}}
			w := get(serversRouter(lister), "/v1/admin/servers", tc.auth)
			if w.Code != tc.status {
				t.Fatalf("status = %d, quería %d", w.Code, tc.status)
			}
			if lister.called {
				t.Error("el servicio no debe ejecutarse sin autorización")
			}
		})
	}
}
