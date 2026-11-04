package handler

import (
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

func suspensionRequest(r http.Handler, method, auth string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/v1/admin/servers/"+serverID+"/suspension", nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSuspendServerReturnsTheUpdatedServer(t *testing.T) {
	at := time.Date(2026, 11, 6, 9, 30, 0, 0, time.UTC)
	owner := "Manu"
	m := &fakeModerator{server: &model.Server{
		ID: serverID, Name: "Sala", OwnerID: userID, OwnerName: &owner, MemberCount: 3,
		CreatedAt: time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC), SuspendedAt: &at,
	}}
	w := suspensionRequest(moderationRouter(m), http.MethodPost, bearer(t, staffID))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["id"] != serverID || body["name"] != "Sala" || body["owner_name"] != "Manu" || body["member_count"] != float64(3) || body["suspended_at"] != "2026-11-06T09:30:00Z" {
		t.Errorf("body: %v", body)
	}
	if m.action != "suspend" || m.gotServer != serverID || m.gotStaff != staffID {
		t.Errorf("el servicio recibió %s server=%q staff=%q, quería el del path y el del token", m.action, m.gotServer, m.gotStaff)
	}
}

func TestReactivateServerReturnsTheServerWithoutSuspension(t *testing.T) {
	m := &fakeModerator{server: &model.Server{ID: serverID, Name: "Sala", OwnerID: userID, CreatedAt: time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)}}
	w := suspensionRequest(moderationRouter(m), http.MethodDelete, bearer(t, staffID))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if v, present := body["suspended_at"]; !present || v != nil {
		t.Errorf("suspended_at debe estar presente y ser null, no %v", v)
	}
	if m.action != "reactivate" || m.gotServer != serverID {
		t.Errorf("el servicio recibió %s server=%q", m.action, m.gotServer)
	}
}

func TestSuspensionEndpointsMapBusinessErrors(t *testing.T) {
	cases := []struct {
		method string
		err    error
		status int
		code   string
	}{
		{http.MethodPost, service.ErrServerNotFound, http.StatusNotFound, apierr.CodeServerNotFound},
		{http.MethodPost, service.ErrServerAlreadySuspended, http.StatusConflict, apierr.CodeServerAlreadySuspended},
		{http.MethodPost, fmt.Errorf("suspender el servidor: %w", service.ErrServerAlreadySuspended), http.StatusConflict, apierr.CodeServerAlreadySuspended},
		{http.MethodDelete, service.ErrServerNotFound, http.StatusNotFound, apierr.CodeServerNotFound},
		{http.MethodDelete, service.ErrServerNotSuspended, http.StatusConflict, apierr.CodeServerNotSuspended},
		{http.MethodDelete, fmt.Errorf("reactivar el servidor: %w", service.ErrServerNotSuspended), http.StatusConflict, apierr.CodeServerNotSuspended},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s/%s/%v", tc.method, tc.code, tc.err), func(t *testing.T) {
			w := suspensionRequest(moderationRouter(&fakeModerator{err: tc.err}), tc.method, bearer(t, staffID))
			if e := errorBody(t, w); w.Code != tc.status || e.Code != tc.code {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestSuspensionEndpointsUnexpectedErrorIsInternal(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			w := suspensionRequest(moderationRouter(&fakeModerator{err: errors.New("db caída")}), method, bearer(t, staffID))
			if e := errorBody(t, w); w.Code != http.StatusInternalServerError || e.Code != apierr.CodeInternal {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "db caída") {
				t.Error("el detalle del error interno no debe llegar al cliente")
			}
		})
	}
}

// Sin token, o con el token de alguien que no es staff, no se ejecuta nada.
func TestSuspensionEndpointsRequireStaff(t *testing.T) {
	cases := []struct {
		name, auth string
		status     int
	}{
		{"sin token", "", http.StatusUnauthorized},
		{"no es staff", bearer(t, userID), http.StatusForbidden},
	}
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		for _, tc := range cases {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				m := &fakeModerator{server: &model.Server{}}
				w := suspensionRequest(moderationRouter(m), method, tc.auth)
				if w.Code != tc.status {
					t.Fatalf("status = %d, quería %d", w.Code, tc.status)
				}
				if m.called {
					t.Error("el servicio no debe ejecutarse sin autorización")
				}
			})
		}
	}
}
