package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/admin/internal/service"
	"github.com/discordia-grupo01/admin/internal/testutil"
	"github.com/discordia-grupo01/events-contract/admin"
)

// Recorre handler, servicio y repositorio con Postgres real: el pedido del staff
// termina en un evento firmado en el outbox y el servidor solo sale del listado
// cuando servers confirma el borrado.
func TestDeleteServerFlowWithRealDatabase(t *testing.T) {
	const flowSecret = "secreto-del-flujo"
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "servers", "server_members", "users", "outbox_events")

	servers := repository.NewServerRepository(db)
	router := moderationRouter(service.NewServerModerationService(repository.NewServerModerationRepository(db), flowSecret))
	ctx := context.Background()

	created := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	if err := servers.UpsertServer(ctx, repository.ServerSnapshot{
		ServerID: serverID, Name: "Sala de prueba", OwnerID: userID, CreatedAt: created, OccurredAt: created,
	}); err != nil {
		t.Fatal(err)
	}

	// Un nombre equivocado no escribe nada.
	if w := deleteServer(router, bearer(t, staffID), `{"confirm_name":"otra"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("nombre equivocado: status = %d: %s", w.Code, w.Body.String())
	}
	if n := countOutbox(t, db); n != 0 {
		t.Fatalf("un nombre equivocado no debe escribir eventos, hay %d", n)
	}

	// Con el nombre correcto: 202 y un evento firmado en el outbox.
	if w := deleteServer(router, bearer(t, staffID), `{"confirm_name":" Sala de prueba "}`); w.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var key string
	var payload []byte
	if err := db.QueryRow(`SELECT routing_key, payload FROM outbox_events`).Scan(&key, &payload); err != nil {
		t.Fatal(err)
	}
	if key != admin.RoutingKeyServerDeletionRequested {
		t.Errorf("routing key = %q", key)
	}
	var e admin.ServerDeletionRequested
	if err := json.Unmarshal(payload, &e); err != nil {
		t.Fatal(err)
	}
	if e.ServerID != serverID || e.Version != 1 || !e.Verify(flowSecret) {
		t.Errorf("evento: %+v (verifica=%v)", e, e.Verify(flowSecret))
	}
	var requestedBy string
	if err := db.QueryRow(`SELECT deletion_requested_by::text FROM servers WHERE server_id = $1`, serverID).Scan(&requestedBy); err != nil || requestedBy != staffID {
		t.Errorf("deletion_requested_by = %q (%v), quería %q", requestedBy, err, staffID)
	}

	// Hasta que servers confirme, sigue en el listado.
	if _, total, err := servers.List(ctx, repository.ServerFilter{}, 10, 0); err != nil || total != 1 {
		t.Fatalf("antes de la confirmación: total=%d err=%v", total, err)
	}
	if err := servers.MarkDeleted(ctx, serverID, time.Now().UTC()); err != nil { // llega servers.server_deleted
		t.Fatal(err)
	}
	if _, total, err := servers.List(ctx, repository.ServerFilter{}, 10, 0); err != nil || total != 0 {
		t.Errorf("después de la confirmación: total=%d err=%v", total, err)
	}

	// Ya borrado, no se puede volver a eliminar.
	if w := deleteServer(router, bearer(t, staffID), `{"confirm_name":"Sala de prueba"}`); w.Code != http.StatusNotFound {
		t.Errorf("servidor ya borrado: status = %d, quería 404", w.Code)
	}
}

func countOutbox(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM outbox_events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
