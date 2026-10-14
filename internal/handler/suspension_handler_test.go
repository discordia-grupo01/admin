package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/apierr"
	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/service"
)

type fakeSuspender struct {
	user    *model.User
	err     error
	called  bool
	gotUser string
	gotBy   string
}

func (f *fakeSuspender) Suspend(_ context.Context, userID, staffID string) (*model.User, error) {
	f.called = true
	f.gotUser, f.gotBy = userID, staffID
	return f.user, f.err
}

func suspensionRouter(s UserSuspender) http.Handler {
	return NewRouter(Deps{JWTSecret: []byte(secret), Staff: fakeStaff{}, Health: NewHealthHandler()},
		NewSuspensionHandler(s).Register)
}

func post(r http.Handler, path, auth string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

const targetID = "7c1e5a3b-9d2f-4b8e-a6c4-3f1e9d7b5a2c"

func TestSuspendReturnsTheUpdatedUser(t *testing.T) {
	at := time.Date(2026, 11, 2, 15, 0, 0, 0, time.UTC)
	s := &fakeSuspender{user: &model.User{
		ID: targetID, Name: "Ale", Email: "ale@example.com", Status: model.UserStatusSuspended,
		CreatedAt: time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC), SuspendedAt: &at,
	}}
	w := post(suspensionRouter(s), "/v1/admin/users/"+targetID+"/suspension", bearer(t, staffID))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["id"] != targetID || body["status"] != "suspended" || body["suspended_at"] != "2026-11-02T15:00:00Z" {
		t.Errorf("body: %v", body)
	}
	if s.gotUser != targetID || s.gotBy != staffID {
		t.Errorf("el servicio recibió user=%q staff=%q, quería el del path y el del token", s.gotUser, s.gotBy)
	}
}

func TestSuspendMapsBusinessErrors(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{service.ErrUserNotFound, http.StatusNotFound, apierr.CodeUserNotFound},
		{service.ErrCannotSuspendStaff, http.StatusForbidden, apierr.CodeCannotSuspendStaff},
		{service.ErrUserAlreadySuspended, http.StatusConflict, apierr.CodeUserAlreadySuspended},
		{fmt.Errorf("suspender usuario: %w", service.ErrUserAlreadySuspended), http.StatusConflict, apierr.CodeUserAlreadySuspended},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			w := post(suspensionRouter(&fakeSuspender{err: tc.err}), "/v1/admin/users/"+targetID+"/suspension", bearer(t, staffID))
			var body apierr.ErrorResponse
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			if w.Code != tc.status || body.Error.Code != tc.code {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestSuspendUnexpectedErrorIsInternal(t *testing.T) {
	w := post(suspensionRouter(&fakeSuspender{err: errors.New("db caída")}), "/v1/admin/users/"+targetID+"/suspension", bearer(t, staffID))
	var body apierr.ErrorResponse
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != http.StatusInternalServerError || body.Error.Code != apierr.CodeInternal {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "db caída") {
		t.Error("el detalle del error interno no debe llegar al cliente")
	}
}

// Sin token, o con el token de alguien que no es staff, no se ejecuta nada (CA3).
func TestSuspendRequiresStaff(t *testing.T) {
	cases := []struct {
		name, auth string
		status     int
	}{
		{"sin token", "", http.StatusUnauthorized},
		{"no es staff", bearer(t, userID), http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &fakeSuspender{user: &model.User{}}
			w := post(suspensionRouter(s), "/v1/admin/users/"+targetID+"/suspension", tc.auth)
			if w.Code != tc.status {
				t.Fatalf("status = %d, quería %d", w.Code, tc.status)
			}
			if s.called {
				t.Error("el servicio no debe ejecutarse sin autorización")
			}
		})
	}
}
