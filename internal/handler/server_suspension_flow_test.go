package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/admin/internal/service"
	"github.com/discordia-grupo01/admin/internal/testutil"
	"github.com/discordia-grupo01/events-contract/admin"
)

// Recorre handler, servicio y repositorio con Postgres real: suspender y
// reactivar dejan eventos firmados con versiones crecientes y el listado refleja
// el estado de cada momento.
func TestSuspendAndReactivateServerFlowWithRealDatabase(t *testing.T) {
	const flowSecret = "secreto-del-flujo"
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "servers", "server_members", "users", "outbox_events")

	servers := repository.NewServerRepository(db)
	router := NewRouter(Deps{JWTSecret: []byte(secret), Staff: fakeStaff{}, Health: NewHealthHandler()},
		NewServerHandler(service.NewServerService(servers)).Register,
		NewServerModerationHandler(service.NewServerModerationService(repository.NewServerModerationRepository(db), flowSecret)).Register)

	created := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	if err := servers.UpsertServer(context.Background(), repository.ServerSnapshot{
		ServerID: serverID, Name: "Sala de prueba", OwnerID: userID, CreatedAt: created, OccurredAt: created,
	}); err != nil {
		t.Fatal(err)
	}
	auth := bearer(t, staffID)

	listed := func(status string) int {
		t.Helper()
		w := get(router, "/v1/admin/servers?status="+status, auth)
		if w.Code != http.StatusOK {
			t.Fatalf("listar con status=%q: %d %s", status, w.Code, w.Body.String())
		}
		var body struct {
			Total int `json:"total"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Total
	}
	outboxEvent := func(seq int) (string, []byte) {
		t.Helper()
		var key string
		var payload []byte
		if err := db.QueryRow(`SELECT routing_key, payload FROM outbox_events ORDER BY seq OFFSET $1 LIMIT 1`, seq).Scan(&key, &payload); err != nil {
			t.Fatalf("evento %d del outbox: %v", seq, err)
		}
		return key, payload
	}

	if listed("active") != 1 || listed("suspended") != 0 {
		t.Fatal("al principio el servidor está activo")
	}

	// Suspender.
	w := suspensionRequest(router, http.MethodPost, auth)
	if w.Code != http.StatusOK {
		t.Fatalf("suspender: %d %s", w.Code, w.Body.String())
	}
	var suspended struct {
		SuspendedAt *time.Time `json:"suspended_at"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &suspended); err != nil || suspended.SuspendedAt == nil {
		t.Fatalf("la respuesta tiene que traer suspended_at: %s (%v)", w.Body.String(), err)
	}
	key, payload := outboxEvent(0)
	var s admin.ServerSuspended
	if err := json.Unmarshal(payload, &s); err != nil {
		t.Fatal(err)
	}
	if key != admin.RoutingKeyServerSuspended || s.ServerID != serverID || s.Version != 1 || !s.Verify(flowSecret) || !s.OccurredAt.Equal(*suspended.SuspendedAt) {
		t.Errorf("evento de suspensión: key=%q %+v (verifica=%v)", key, s, s.Verify(flowSecret))
	}
	if listed("active") != 0 || listed("suspended") != 1 {
		t.Error("tras suspender, el servidor figura como suspendido y sigue en el listado")
	}

	// Suspender de nuevo: conflicto y sin evento nuevo.
	if w := suspensionRequest(router, http.MethodPost, auth); w.Code != http.StatusConflict {
		t.Errorf("segunda suspensión: status = %d, quería 409", w.Code)
	}
	if n := countOutbox(t, db); n != 1 {
		t.Errorf("hay %d eventos en el outbox, quería 1", n)
	}

	// Reactivar.
	if w := suspensionRequest(router, http.MethodDelete, auth); w.Code != http.StatusOK {
		t.Fatalf("reactivar: %d %s", w.Code, w.Body.String())
	}
	key, payload = outboxEvent(1)
	var r admin.ServerReactivated
	if err := json.Unmarshal(payload, &r); err != nil {
		t.Fatal(err)
	}
	if key != admin.RoutingKeyServerReactivated || r.Version != 2 || !r.Verify(flowSecret) {
		t.Errorf("evento de reactivación: key=%q %+v", key, r)
	}
	if listed("active") != 1 || listed("suspended") != 0 {
		t.Error("tras reactivar, el servidor vuelve a figurar como activo")
	}

	// Reactivar un servidor activo: conflicto.
	if w := suspensionRequest(router, http.MethodDelete, auth); w.Code != http.StatusConflict {
		t.Errorf("reactivar uno activo: status = %d, quería 409", w.Code)
	}
}
