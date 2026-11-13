package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/admin/internal/service"
	"github.com/discordia-grupo01/admin/internal/testutil"
	"github.com/discordia-grupo01/events-contract/admin"
)

// Recorre consumidor, servicio, repositorio y handler con Postgres real: la
// solicitud que llega de identify aparece en el listado y en el expediente, se
// aprueba una sola vez y deja el evento firmado para identify.
func TestAccountRecoveryFlowWithRealDatabase(t *testing.T) {
	const flowSecret = "secreto-del-flujo"
	const olderRequestID = "44444444-4444-4444-4444-444444444444"
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "account_recovery_requests", "users", "outbox_events")
	ctx := context.Background()

	userRepo := repository.NewUserRepository(db)
	registered := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	for id, name := range map[string]string{userID: "Ana", staffID: "Martina Ríos"} {
		if err := userRepo.UpsertRegistration(ctx, repository.Registration{
			UserID: id, Email: name + "@example.com", Name: name, RegisteredAt: registered, EmailVerified: true,
		}); err != nil {
			t.Fatal(err)
		}
	}

	recoveryRepo := repository.NewRecoveryRequestRepository(db)
	recovery := service.NewRecoveryService(recoveryRepo, flowSecret)
	router := NewRouter(Deps{JWTSecret: []byte(secret), Staff: fakeStaff{}, Health: NewHealthHandler()},
		NewRecoveryHandler(recovery).Register,
		NewDossierHandler(service.NewDossierService(repository.NewDossierRepository(db)).WithRecoveryRequests(recoveryRepo)).Register)
	auth := bearer(t, staffID)

	// Una solicitud vieja ya rechazada y la actual, pendiente. La actual llega dos
	// veces (entrega repetida) y no se duplica.
	older := repository.RecoveryRequestRecord{RequestID: olderRequestID, UserID: userID, RequestedAt: registered.Add(24 * time.Hour)}
	current := repository.RecoveryRequestRecord{RequestID: recoveryRequestID, UserID: userID, Message: "cambié de celular", RequestedAt: registered.Add(48 * time.Hour)}
	for _, rec := range []repository.RecoveryRequestRecord{older, current, current} {
		if err := recovery.ApplyRequest(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := recovery.Reject(ctx, olderRequestID, staffID); err != nil {
		t.Fatal(err)
	}

	list := func(query string) model.RecoveryRequestList {
		t.Helper()
		w := get(router, "/v1/admin/recovery-requests"+query, auth)
		if w.Code != http.StatusOK {
			t.Fatalf("listar %q: %d %s", query, w.Code, w.Body.String())
		}
		var body model.RecoveryRequestList
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	pending := list("?status=pending")
	if pending.Total != 1 || pending.Requests[0].ID != recoveryRequestID {
		t.Fatalf("pendientes = %+v", pending)
	}
	got := pending.Requests[0]
	if got.User == nil || got.User.Name != "Ana" || got.User.Email != "Ana@example.com" || got.Message != "cambié de celular" || got.ResolvedBy != nil {
		t.Errorf("solicitud pendiente = %+v (usuario %+v)", got, got.User)
	}
	if all := list(""); all.Total != 2 {
		t.Errorf("todas = %d, quería 2", all.Total)
	}
	if found := list("?search=ana&status=rejected"); found.Total != 1 || found.Requests[0].ID != olderRequestID {
		t.Errorf("búsqueda de rechazadas = %+v", found)
	}
	if none := list("?search=nadie"); none.Total != 0 || none.Requests == nil {
		t.Errorf("búsqueda sin resultados = %+v", none)
	}

	// Detalle con las solicitudes anteriores.
	w := get(router, "/v1/admin/recovery-requests/"+recoveryRequestID, auth)
	var detail model.RecoveryRequestDetail
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || w.Code != http.StatusOK {
		t.Fatalf("detalle: %d %s", w.Code, w.Body.String())
	}
	if detail.Previous.Total != 1 || detail.Previous.Rejected != 1 {
		t.Errorf("anteriores = %+v, quería 1 rechazada", detail.Previous)
	}

	// Aprobar: queda resuelta por el staff y sale el evento firmado.
	outboxBefore := countOutbox(t, db)
	w = post(router, "/v1/admin/recovery-requests/"+recoveryRequestID+"/approval", auth)
	if w.Code != http.StatusOK {
		t.Fatalf("aprobar: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Status != model.RecoveryRequestApproved || detail.ResolvedAt == nil || detail.ResolvedBy == nil ||
		detail.ResolvedBy.ID != staffID || detail.ResolvedBy.Name == nil || *detail.ResolvedBy.Name != "Martina Ríos" {
		t.Errorf("aprobada = %+v (resuelta por %+v)", detail, detail.ResolvedBy)
	}

	var key string
	var payload []byte
	if err := db.QueryRow(`SELECT routing_key, payload FROM outbox_events ORDER BY seq DESC LIMIT 1`).Scan(&key, &payload); err != nil {
		t.Fatal(err)
	}
	var approved admin.AccountRecoveryApproved
	if err := json.Unmarshal(payload, &approved); err != nil {
		t.Fatal(err)
	}
	if key != admin.RoutingKeyAccountRecoveryApproved || approved.RequestID != recoveryRequestID || approved.UserID != userID || !approved.Verify(flowSecret) {
		t.Errorf("evento: key=%q %+v (verifica=%v)", key, approved, approved.Verify(flowSecret))
	}

	// Ya resuelta: ni aprobar ni rechazar de nuevo, y sin eventos nuevos.
	for _, action := range []string{"approval", "rejection"} {
		if w := post(router, "/v1/admin/recovery-requests/"+recoveryRequestID+"/"+action, auth); w.Code != http.StatusConflict {
			t.Errorf("%s de una resuelta: status = %d, quería 409", action, w.Code)
		}
	}
	if n := countOutbox(t, db); n != outboxBefore+1 {
		t.Errorf("eventos nuevos = %d, quería 1", n-outboxBefore)
	}
	if w := post(router, "/v1/admin/recovery-requests/no-es-un-uuid/approval", auth); w.Code != http.StatusNotFound {
		t.Errorf("id inválido: status = %d, quería 404", w.Code)
	}

	// Expediente: sección propia, fuera de moderación.
	w = get(router, "/v1/admin/users/"+userID+"/dossier", auth)
	var summary model.UserDossierSummary
	if err := json.Unmarshal(w.Body.Bytes(), &summary); err != nil || w.Code != http.StatusOK {
		t.Fatalf("expediente: %d %s", w.Code, w.Body.String())
	}
	if summary.Counts.RecoveryRequests != 2 || summary.Counts.Moderation != 0 ||
		len(summary.Latest.RecoveryRequests) != 2 || summary.Latest.RecoveryRequests[0].Status != model.RecoveryRequestApproved {
		t.Errorf("expediente: counts=%+v latest=%+v", summary.Counts, summary.Latest.RecoveryRequests)
	}
	w = get(router, "/v1/admin/users/"+userID+"/dossier/recovery_requests?limit=1&offset=1", auth)
	var section model.DossierPage[model.DossierRecoveryRequest]
	if err := json.Unmarshal(w.Body.Bytes(), &section); err != nil || w.Code != http.StatusOK {
		t.Fatalf("sección: %d %s", w.Code, w.Body.String())
	}
	if section.Total != 2 || len(section.Items) != 1 || section.Items[0].ID != olderRequestID {
		t.Errorf("sección paginada = %+v", section)
	}
}

// Si dos miembros del staff resuelven la misma solicitud a la vez, solo uno gana.
func TestAccountRecoveryConcurrentResolutionWithRealDatabase(t *testing.T) {
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "account_recovery_requests", "outbox_events")
	ctx := context.Background()
	recovery := service.NewRecoveryService(repository.NewRecoveryRequestRepository(db), "secreto")
	if err := recovery.ApplyRequest(ctx, repository.RecoveryRequestRecord{
		RequestID: recoveryRequestID, UserID: userID, RequestedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	results := make(chan error, 2)
	go func() { _, err := recovery.Approve(ctx, recoveryRequestID, staffID); results <- err }()
	go func() { _, err := recovery.Reject(ctx, recoveryRequestID, staffID); results <- err }()

	var won, lost int
	for range 2 {
		switch err := <-results; {
		case err == nil:
			won++
		case errors.Is(err, service.ErrRecoveryRequestResolved):
			lost++
		default:
			t.Fatalf("error inesperado: %v", err)
		}
	}
	if won != 1 || lost != 1 || countOutbox(t, db) != 1 {
		t.Errorf("ganaron %d, perdieron %d, eventos %d", won, lost, countOutbox(t, db))
	}
}
