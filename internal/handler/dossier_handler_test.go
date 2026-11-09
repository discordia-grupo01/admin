package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/service"
)

type fakeDossier struct {
	err    error
	called int

	gotID, gotSection   string
	gotLimit, gotOffset int
}

func (f *fakeDossier) UserSummary(_ context.Context, id string) (*model.UserDossierSummary, error) {
	f.called++
	f.gotID = id
	if f.err != nil {
		return nil, f.err
	}
	last := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	return &model.UserDossierSummary{
		User: &model.User{ID: id, Name: "Ale", Status: model.UserStatusActive}, LastActivityAt: &last,
		Counts: model.UserDossierCounts{Servers: 1},
		Latest: model.UserDossierLatest{
			Servers: []model.DossierMembership{}, Activity: []model.DossierActivity{},
			Reports: []model.DossierReport{}, Moderation: []model.DossierModerationAction{},
		},
	}, nil
}

func (f *fakeDossier) UserSection(_ context.Context, id, section string, limit, offset int) (any, error) {
	f.called++
	f.gotID, f.gotSection, f.gotLimit, f.gotOffset = id, section, limit, offset
	if f.err != nil {
		return nil, f.err
	}
	return model.DossierPage[model.DossierReport]{Items: []model.DossierReport{}, Total: 0, Limit: limit, Offset: offset}, nil
}

func (f *fakeDossier) ServerSummary(_ context.Context, id string) (*model.ServerDossierSummary, error) {
	f.called++
	f.gotID = id
	if f.err != nil {
		return nil, f.err
	}
	return &model.ServerDossierSummary{
		Server: &model.Server{ID: id, Name: "Sala"}, Messaging: []model.MessagingDay{{Date: "2026-11-08", Messages: 3}},
		Latest: model.ServerDossierLatest{Reports: []model.DossierReport{}, Moderation: []model.DossierModerationAction{}},
	}, nil
}

func (f *fakeDossier) ServerSection(_ context.Context, id, section string, limit, offset int) (any, error) {
	f.called++
	f.gotID, f.gotSection, f.gotLimit, f.gotOffset = id, section, limit, offset
	if f.err != nil {
		return nil, f.err
	}
	return model.DossierPage[model.DossierModerationAction]{Items: []model.DossierModerationAction{}, Limit: limit, Offset: offset}, nil
}

func dossierRouter(d DossierReader) http.Handler {
	return NewRouter(Deps{JWTSecret: []byte(secret), Staff: fakeStaff{}, Health: NewHealthHandler()}, NewDossierHandler(d).Register)
}

var dossierRoutes = []string{
	"/v1/admin/users/" + targetID + "/dossier",
	"/v1/admin/users/" + targetID + "/dossier/activity",
	"/v1/admin/servers/" + targetID + "/dossier",
	"/v1/admin/servers/" + targetID + "/dossier/reports",
}

// CA3: sin permisos de staff no se lee nada.
func TestDossierRoutesRequireStaff(t *testing.T) {
	for _, path := range dossierRoutes {
		t.Run(path, func(t *testing.T) {
			f := &fakeDossier{}
			r := dossierRouter(f)
			if w := get(r, path, ""); w.Code != http.StatusUnauthorized {
				t.Errorf("sin token: status = %d, quería 401", w.Code)
			}
			if w := get(r, path, "Bearer basura"); w.Code != http.StatusUnauthorized {
				t.Errorf("token inválido: status = %d, quería 401", w.Code)
			}
			w := get(r, path, bearer(t, userID)) // un usuario válido que no es staff
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"FORBIDDEN"`) {
				t.Errorf("no staff: status = %d (%s), quería 403 FORBIDDEN", w.Code, w.Body.String())
			}
			if f.called != 0 {
				t.Error("un no-staff no debe llegar al servicio")
			}
			if w := get(r, path, bearer(t, staffID)); w.Code != http.StatusOK {
				t.Errorf("staff: status = %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestUserSummaryResponse(t *testing.T) {
	w := get(dossierRouter(&fakeDossier{}), dossierRoutes[0], bearer(t, staffID))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"user", "last_activity_at", "counts", "latest"} {
		if _, ok := body[k]; !ok {
			t.Errorf("falta %q en %s", k, w.Body.String())
		}
	}
	latest := body["latest"].(map[string]any)
	for _, k := range []string{"servers", "activity", "reports", "moderation"} {
		if items, ok := latest[k].([]any); !ok || items == nil {
			t.Errorf("latest.%s tiene que ser una lista vacía, no null: %s", k, w.Body.String())
		}
	}
}

func TestServerSummaryResponse(t *testing.T) {
	w := get(dossierRouter(&fakeDossier{}), dossierRoutes[2], bearer(t, staffID))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Server    map[string]any   `json:"server"`
		Messaging []map[string]any `json:"messaging"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Server["name"] != "Sala" || len(body.Messaging) != 1 || body.Messaging[0]["date"] != "2026-11-08" || body.Messaging[0]["messages"] != float64(3) {
		t.Errorf("respuesta = %s", w.Body.String())
	}
}

func TestSectionsPassTheirPaginationToTheService(t *testing.T) {
	f := &fakeDossier{}
	r := dossierRouter(f)

	if w := get(r, "/v1/admin/users/"+targetID+"/dossier/moderation?limit=7&offset=14", bearer(t, staffID)); w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if f.gotID != targetID || f.gotSection != "moderation" || f.gotLimit != 7 || f.gotOffset != 14 {
		t.Errorf("servicio recibió %+v", f)
	}

	w := get(r, "/v1/admin/servers/"+targetID+"/dossier/reports", bearer(t, staffID))
	var page struct {
		Items  []any `json:"items"`
		Total  int   `json:"total"`
		Limit  int   `json:"limit"`
		Offset int   `json:"offset"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if f.gotSection != "reports" || f.gotLimit != model.DefaultLimit || f.gotOffset != 0 || page.Items == nil || page.Limit != model.DefaultLimit {
		t.Errorf("defaults: servicio %+v, página %+v", f, page)
	}
}

func TestInvalidPaginationIsA400(t *testing.T) {
	f := &fakeDossier{}
	r := dossierRouter(f)
	for _, q := range []string{"limit=-1", "limit=abc", "offset=-5", "offset=x"} {
		w := get(r, "/v1/admin/users/"+targetID+"/dossier/activity?"+q, bearer(t, staffID))
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"INVALID_QUERY"`) {
			t.Errorf("%s: status = %d (%s), quería 400 INVALID_QUERY", q, w.Code, w.Body.String())
		}
	}
	if f.called != 0 {
		t.Error("no debió llegar al servicio")
	}
}

func TestDossierErrors(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		path   string
		status int
		code   string
	}{
		{"usuario inexistente", service.ErrUserNotFound, dossierRoutes[0], http.StatusNotFound, "USER_NOT_FOUND"},
		{"sección de usuario inexistente", service.ErrUserNotFound, dossierRoutes[1], http.StatusNotFound, "USER_NOT_FOUND"},
		{"servidor inexistente", service.ErrServerNotFound, dossierRoutes[2], http.StatusNotFound, "SERVER_NOT_FOUND"},
		{"sección inválida", service.ErrInvalidSection, dossierRoutes[3], http.StatusBadRequest, "INVALID_SECTION"},
		{"falla de base", errors.New("db caída"), dossierRoutes[0], http.StatusInternalServerError, "INTERNAL_ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := get(dossierRouter(&fakeDossier{err: tc.err}), tc.path, bearer(t, staffID))
			if w.Code != tc.status || !strings.Contains(w.Body.String(), `"`+tc.code+`"`) {
				t.Errorf("status = %d (%s), quería %d %s", w.Code, w.Body.String(), tc.status, tc.code)
			}
			if strings.Contains(w.Body.String(), "db caída") {
				t.Error("el error interno no debe filtrarse al cliente")
			}
		})
	}
}
