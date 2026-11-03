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

	"github.com/discordia-grupo01/admin/internal/apierr"
	"github.com/discordia-grupo01/admin/internal/service"
)

type fakeModerator struct {
	err error

	called     bool
	gotServer  string
	gotConfirm string
	gotStaff   string
}

func (f *fakeModerator) Delete(_ context.Context, serverID, confirmName, staffID string) error {
	f.called = true
	f.gotServer, f.gotConfirm, f.gotStaff = serverID, confirmName, staffID
	return f.err
}

func moderationRouter(m ServerModerator) http.Handler {
	return NewRouter(Deps{JWTSecret: []byte(secret), Staff: fakeStaff{}, Health: NewHealthHandler()},
		NewServerModerationHandler(m).Register)
}

const serverID = "c3d4e5f6-0a1b-4c2d-9e3f-5a6b7c8d9e0f"

func deleteServer(r http.Handler, auth, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, "/v1/admin/servers/"+serverID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func errorBody(t *testing.T, w *httptest.ResponseRecorder) apierr.Body {
	t.Helper()
	var body apierr.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("la respuesta no es un error JSON: %v (%s)", err, w.Body.String())
	}
	return body.Error
}

func TestDeleteServerIsAcceptedWithoutBody(t *testing.T) {
	m := &fakeModerator{}
	w := deleteServer(moderationRouter(m), bearer(t, staffID), `{"confirm_name":"Sala"}`)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Errorf("el 202 no lleva cuerpo: %q", w.Body.String())
	}
	if m.gotServer != serverID || m.gotConfirm != "Sala" || m.gotStaff != staffID {
		t.Errorf("el servicio recibió server=%q confirm=%q staff=%q, quería el del path, el del cuerpo y el del token",
			m.gotServer, m.gotConfirm, m.gotStaff)
	}
}

func TestDeleteServerMapsBusinessErrors(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{service.ErrServerNotFound, http.StatusNotFound, apierr.CodeServerNotFound},
		{service.ErrDeleteConfirmationMismatch, http.StatusBadRequest, apierr.CodeDeleteConfirmationMismatch},
		{fmt.Errorf("pedir la eliminación: %w", service.ErrServerNotFound), http.StatusNotFound, apierr.CodeServerNotFound},
		{fmt.Errorf("pedir la eliminación: %w", service.ErrDeleteConfirmationMismatch), http.StatusBadRequest, apierr.CodeDeleteConfirmationMismatch},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s/%v", tc.code, tc.err), func(t *testing.T) {
			w := deleteServer(moderationRouter(&fakeModerator{err: tc.err}), bearer(t, staffID), `{"confirm_name":"Sala"}`)
			if e := errorBody(t, w); w.Code != tc.status || e.Code != tc.code {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestDeleteServerMismatchPointsAtTheField(t *testing.T) {
	w := deleteServer(moderationRouter(&fakeModerator{err: service.ErrDeleteConfirmationMismatch}), bearer(t, staffID), `{"confirm_name":"x"}`)
	if e := errorBody(t, w); e.Details["field"] != "confirm_name" {
		t.Errorf("details = %v, quería field=confirm_name", e.Details)
	}
}

// Sin cuerpo o sin confirm_name no se borra: el servicio recibe un nombre vacío,
// que nunca coincide con el de un servidor.
func TestDeleteServerWithoutBodyPassesAnEmptyConfirmation(t *testing.T) {
	for name, body := range map[string]string{"sin cuerpo": "", "objeto vacío": `{}`, "nombre null": `{"confirm_name":null}`} {
		t.Run(name, func(t *testing.T) {
			m := &fakeModerator{err: service.ErrDeleteConfirmationMismatch}
			w := deleteServer(moderationRouter(m), bearer(t, staffID), body)

			if !m.called || m.gotConfirm != "" {
				t.Errorf("called=%v confirm=%q: tenía que llegar vacío al servicio", m.called, m.gotConfirm)
			}
			if e := errorBody(t, w); w.Code != http.StatusBadRequest || e.Code != apierr.CodeDeleteConfirmationMismatch {
				t.Errorf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestDeleteServerMalformedBodyIsBadRequest(t *testing.T) {
	for name, body := range map[string]string{
		"json roto":       `{"confirm_name":`,
		"no es un objeto": `"Sala"`,
		"tipo equivocado": `{"confirm_name":123}`,
	} {
		t.Run(name, func(t *testing.T) {
			m := &fakeModerator{}
			w := deleteServer(moderationRouter(m), bearer(t, staffID), body)

			if e := errorBody(t, w); w.Code != http.StatusBadRequest || e.Code != apierr.CodeInvalidBody {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if m.called {
				t.Error("con un cuerpo inválido no se debe llamar al servicio")
			}
		})
	}
}

func TestDeleteServerUnexpectedErrorIsInternal(t *testing.T) {
	w := deleteServer(moderationRouter(&fakeModerator{err: errors.New("db caída")}), bearer(t, staffID), `{"confirm_name":"Sala"}`)

	if e := errorBody(t, w); w.Code != http.StatusInternalServerError || e.Code != apierr.CodeInternal {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "db caída") {
		t.Error("el detalle del error interno no debe llegar al cliente")
	}
}

// Sin token, o con el token de alguien que no es staff, no se ejecuta nada (CA2).
func TestDeleteServerRequiresStaff(t *testing.T) {
	cases := []struct {
		name, auth string
		status     int
	}{
		{"sin token", "", http.StatusUnauthorized},
		{"no es staff", bearer(t, userID), http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &fakeModerator{}
			w := deleteServer(moderationRouter(m), tc.auth, `{"confirm_name":"Sala"}`)

			if w.Code != tc.status {
				t.Fatalf("status = %d, quería %d", w.Code, tc.status)
			}
			if m.called {
				t.Error("el servicio no debe ejecutarse sin autorización")
			}
		})
	}
}
