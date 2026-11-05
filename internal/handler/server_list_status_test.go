package handler

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/apierr"
	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/service"
)

func TestListServersShowsSuspendedAt(t *testing.T) {
	at := time.Date(2026, 11, 6, 9, 30, 0, 0, time.UTC)
	lister := &fakeServerLister{list: &model.ServerList{
		Servers: []model.Server{
			{ID: "s1", Name: "Suspendido", SuspendedAt: &at, CreatedAt: time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)},
			{ID: "s2", Name: "Activo", CreatedAt: time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)},
		},
		Total: 2, Limit: 20,
	}}
	w := get(serversRouter(lister), "/v1/admin/servers", bearer(t, staffID))

	var body struct {
		Servers []map[string]any `json:"servers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.Servers) != 2 {
		t.Fatalf("body: %s (%v)", w.Body.String(), err)
	}
	if body.Servers[0]["suspended_at"] != "2026-11-06T09:30:00Z" {
		t.Errorf("suspended_at = %v", body.Servers[0]["suspended_at"])
	}
	if v, present := body.Servers[1]["suspended_at"]; !present || v != nil {
		t.Errorf("un servidor activo lleva suspended_at null (presente), no %v", v)
	}
}

func TestListServersPassesTheStatusFilter(t *testing.T) {
	for _, status := range []string{"active", "suspended"} {
		t.Run(status, func(t *testing.T) {
			lister := &fakeServerLister{list: &model.ServerList{Servers: []model.Server{}}}
			w := get(serversRouter(lister), "/v1/admin/servers?status="+status+"&search=sala", bearer(t, staffID))

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			if lister.got.Status != model.ServerStatus(status) || lister.got.Search != "sala" {
				t.Errorf("el servicio recibió %+v", lister.got)
			}
		})
	}
}

func TestListServersWithoutStatusDoesNotFilter(t *testing.T) {
	lister := &fakeServerLister{list: &model.ServerList{Servers: []model.Server{}}}
	get(serversRouter(lister), "/v1/admin/servers", bearer(t, staffID))
	if lister.got.Status != "" {
		t.Errorf("sin status no se filtra: %q", lister.got.Status)
	}
}

func TestListServersInvalidStatusIsABadQuery(t *testing.T) {
	lister := &fakeServerLister{err: service.ErrInvalidServerStatus}
	w := get(serversRouter(lister), "/v1/admin/servers?status=pending", bearer(t, staffID))

	if e := errorBody(t, w); w.Code != http.StatusBadRequest || e.Code != apierr.CodeInvalidQuery || e.Details["param"] != "status" {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
