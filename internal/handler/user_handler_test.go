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

type fakeUserLister struct {
	list   *model.UserList
	err    error
	got    service.ListUsersParams
	called bool
}

func (f *fakeUserLister) List(_ context.Context, p service.ListUsersParams) (*model.UserList, error) {
	f.called = true
	f.got = p
	return f.list, f.err
}

func usersRouter(l UserLister) http.Handler {
	return NewRouter(Deps{JWTSecret: []byte(secret), Staff: fakeStaff{}, Health: NewHealthHandler()},
		NewUserHandler(l).Register)
}

func TestListUsersReturnsPage(t *testing.T) {
	suspendedAt := time.Date(2026, 11, 2, 15, 0, 0, 0, time.UTC)
	lister := &fakeUserLister{list: &model.UserList{
		Users: []model.User{
			{ID: "u1", Name: "Manu", Email: "manu@example.com", Status: model.UserStatusActive, CreatedAt: time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)},
			{ID: "u2", Name: "Ale", Email: "ale@example.com", Status: model.UserStatusSuspended, CreatedAt: time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC), SuspendedAt: &suspendedAt},
		},
		Total: 2, Limit: 20, Offset: 0,
	}}
	w := get(usersRouter(lister), "/v1/admin/users", bearer(t, staffID))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Users []map[string]any `json:"users"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 2 || len(body.Users) != 2 {
		t.Fatalf("body: %s", w.Body.String())
	}
	active, suspended := body.Users[0], body.Users[1]
	if active["suspended_at"] != nil {
		t.Errorf("un usuario activo debe llevar suspended_at null, no %v", active["suspended_at"])
	}
	if suspended["suspended_at"] != "2026-11-02T15:00:00Z" || suspended["status"] != "suspended" {
		t.Errorf("usuario suspendido: %v", suspended)
	}
	if active["created_at"] != "2026-09-01T10:00:00Z" {
		t.Errorf("created_at debe ser ISO 8601 en UTC: %v", active["created_at"])
	}
}

func TestListUsersPassesQueryParameters(t *testing.T) {
	lister := &fakeUserLister{list: &model.UserList{Users: []model.User{}}}
	w := get(usersRouter(lister), "/v1/admin/users?search=%20manu%20&status=suspended&limit=10&offset=30", bearer(t, staffID))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	want := service.ListUsersParams{Search: "manu", Status: model.UserStatusSuspended, Limit: 10, Offset: 30}
	if lister.got != want {
		t.Errorf("params = %+v, quería %+v", lister.got, want)
	}
}

func TestListUsersDefaultsWhenNoParameters(t *testing.T) {
	lister := &fakeUserLister{list: &model.UserList{Users: []model.User{}}}
	get(usersRouter(lister), "/v1/admin/users", bearer(t, staffID))
	if lister.got.Limit != model.DefaultLimit || lister.got.Offset != 0 || lister.got.Search != "" || lister.got.Status != "" {
		t.Errorf("params por defecto = %+v", lister.got)
	}
}

func TestListUsersEmptyResultIsAnEmptyList(t *testing.T) {
	lister := &fakeUserLister{list: &model.UserList{Users: []model.User{}, Limit: 20}}
	w := get(usersRouter(lister), "/v1/admin/users?search=nadie", bearer(t, staffID))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"users":[]`) {
		t.Fatalf("un resultado vacío es 200 con users:[], no null ni 404: %d %s", w.Code, w.Body.String())
	}
}

func TestListUsersInvalidParameters(t *testing.T) {
	cases := map[string]string{
		"limit negativo":    "?limit=-1",
		"limit no numérico": "?limit=muchos",
		"offset negativo":   "?offset=-5",
		"search muy largo":  "?search=" + strings.Repeat("a", 101),
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			lister := &fakeUserLister{}
			w := get(usersRouter(lister), "/v1/admin/users"+query, bearer(t, staffID))
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

func TestListUsersInvalidStatus(t *testing.T) {
	lister := &fakeUserLister{err: service.ErrInvalidStatus}
	w := get(usersRouter(lister), "/v1/admin/users?status=banned", bearer(t, staffID))
	var body apierr.ErrorResponse
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != http.StatusBadRequest || body.Error.Code != apierr.CodeInvalidQuery || body.Error.Details["param"] != "status" {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestListUsersServiceErrorIsInternal(t *testing.T) {
	w := get(usersRouter(&fakeUserLister{err: errors.New("db caída")}), "/v1/admin/users", bearer(t, staffID))
	var body apierr.ErrorResponse
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != http.StatusInternalServerError || body.Error.Code != apierr.CodeInternal {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "db caída") {
		t.Error("el detalle del error interno no debe llegar al cliente")
	}
}

// El endpoint hereda la protección del grupo /v1/admin: sin token o sin ser
// staff no se ejecuta nada.
func TestListUsersRequiresStaff(t *testing.T) {
	cases := []struct {
		name, auth string
		status     int
	}{
		{"sin token", "", http.StatusUnauthorized},
		{"no es staff", bearer(t, userID), http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lister := &fakeUserLister{list: &model.UserList{Users: []model.User{}}}
			w := get(usersRouter(lister), "/v1/admin/users", tc.auth)
			if w.Code != tc.status {
				t.Fatalf("status = %d, quería %d", w.Code, tc.status)
			}
			if lister.called {
				t.Error("el servicio no debe ejecutarse sin autorización")
			}
		})
	}
}
