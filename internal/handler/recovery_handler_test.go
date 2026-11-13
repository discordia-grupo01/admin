package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/service"
)

const recoveryRequestID = "33333333-3333-3333-3333-333333333333"

type fakeRecoveryRequests struct {
	list    *model.RecoveryRequestList
	detail  *model.RecoveryRequestDetail
	err     error
	params  service.ListRecoveryRequestsParams
	id      string
	staffID string
	action  string
}

func (f *fakeRecoveryRequests) List(_ context.Context, p service.ListRecoveryRequestsParams) (*model.RecoveryRequestList, error) {
	f.params = p
	return f.list, f.err
}

func (f *fakeRecoveryRequests) Get(_ context.Context, id string) (*model.RecoveryRequestDetail, error) {
	f.id, f.action = id, "get"
	return f.detail, f.err
}

func (f *fakeRecoveryRequests) Approve(_ context.Context, id, staffID string) (*model.RecoveryRequestDetail, error) {
	f.id, f.staffID, f.action = id, staffID, "approve"
	return f.detail, f.err
}

func (f *fakeRecoveryRequests) Reject(_ context.Context, id, staffID string) (*model.RecoveryRequestDetail, error) {
	f.id, f.staffID, f.action = id, staffID, "reject"
	return f.detail, f.err
}

func recoveryRouter(f *fakeRecoveryRequests) http.Handler {
	return NewRouter(Deps{JWTSecret: []byte(secret), Staff: fakeStaff{}, Health: NewHealthHandler()}, NewRecoveryHandler(f).Register)
}

func errorCode(t *testing.T, body []byte) string {
	t.Helper()
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("respuesta de error ilegible: %s", body)
	}
	return payload.Error.Code
}

func TestListRecoveryRequestsPassesTheFilters(t *testing.T) {
	f := &fakeRecoveryRequests{list: &model.RecoveryRequestList{Requests: []model.RecoveryRequest{}, Total: 0, Limit: 10}}

	w := get(recoveryRouter(f), "/v1/admin/recovery-requests?status=pending&search=%20ana%20&limit=10&offset=5", bearer(t, staffID))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	want := service.ListRecoveryRequestsParams{Search: "ana", Status: model.RecoveryRequestPending, Limit: 10, Offset: 5}
	if f.params != want {
		t.Errorf("params = %+v, quería %+v", f.params, want)
	}
}

func TestListRecoveryRequestsRejectsAnUnknownStatus(t *testing.T) {
	f := &fakeRecoveryRequests{err: service.ErrInvalidRecoveryStatus}

	w := get(recoveryRouter(f), "/v1/admin/recovery-requests?status=otra", bearer(t, staffID))

	if w.Code != http.StatusBadRequest || errorCode(t, w.Body.Bytes()) != "INVALID_QUERY" {
		t.Errorf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestRecoveryRequestActionsUseTheStaffFromTheToken(t *testing.T) {
	for path, action := range map[string]string{
		"/v1/admin/recovery-requests/" + recoveryRequestID + "/approval":  "approve",
		"/v1/admin/recovery-requests/" + recoveryRequestID + "/rejection": "reject",
	} {
		f := &fakeRecoveryRequests{detail: &model.RecoveryRequestDetail{RecoveryRequest: model.RecoveryRequest{ID: recoveryRequestID}}}

		w := post(recoveryRouter(f), path, bearer(t, staffID))

		if w.Code != http.StatusOK {
			t.Fatalf("%s: status = %d: %s", action, w.Code, w.Body.String())
		}
		if f.action != action || f.id != recoveryRequestID || f.staffID != staffID {
			t.Errorf("%s: llamada = %s(%s, %s)", action, f.action, f.id, f.staffID)
		}
	}
}

func TestRecoveryRequestErrors(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
		code   string
	}{
		"inexistente":   {service.ErrRecoveryRequestNotFound, http.StatusNotFound, "RECOVERY_REQUEST_NOT_FOUND"},
		"ya resuelta":   {service.ErrRecoveryRequestResolved, http.StatusConflict, "RECOVERY_REQUEST_ALREADY_RESOLVED"},
		"falla interna": {errors.New("db caída"), http.StatusInternalServerError, "INTERNAL_ERROR"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := post(recoveryRouter(&fakeRecoveryRequests{err: tc.err}), "/v1/admin/recovery-requests/"+recoveryRequestID+"/approval", bearer(t, staffID))
			if w.Code != tc.status || errorCode(t, w.Body.Bytes()) != tc.code {
				t.Errorf("status = %d, body = %s", w.Code, w.Body.String())
			}
		})
	}

	w := get(recoveryRouter(&fakeRecoveryRequests{err: service.ErrRecoveryRequestNotFound}), "/v1/admin/recovery-requests/"+recoveryRequestID, bearer(t, staffID))
	if w.Code != http.StatusNotFound {
		t.Errorf("detalle inexistente: status = %d", w.Code)
	}
}

func TestRecoveryRequestsAreStaffOnly(t *testing.T) {
	f := &fakeRecoveryRequests{}
	router := recoveryRouter(f)

	if w := post(router, "/v1/admin/recovery-requests/"+recoveryRequestID+"/approval", bearer(t, userID)); w.Code != http.StatusForbidden {
		t.Errorf("sin ser staff: status = %d, quería 403", w.Code)
	}
	if w := get(router, "/v1/admin/recovery-requests", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("sin token: status = %d, quería 401", w.Code)
	}
	if f.action != "" {
		t.Errorf("no debería llegar al servicio: %s", f.action)
	}
}
