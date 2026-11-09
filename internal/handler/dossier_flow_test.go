package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/admin/internal/service"
	"github.com/discordia-grupo01/admin/internal/testutil"
)

const (
	flowUser    = "aaaaaaaa-0000-4000-8000-000000000001"
	flowOwner   = "aaaaaaaa-0000-4000-8000-000000000002"
	flowServer  = "bbbbbbbb-0000-4000-8000-000000000001"
	flowServer2 = "bbbbbbbb-0000-4000-8000-000000000002"
	flowActor   = "cccccccc-0000-4000-8000-0000000000ff"
)

func flowID(prefix string, n int) string {
	return prefix + "-0000-4000-8000-" + strings.Repeat("0", 11) + string(rune('0'+n))
}

// Recorre handler, servicio y repositorios con Postgres real: se siembra lo que
// dejarían los eventos y las acciones del staff, y se verifica lo que ve el
// staff en el expediente de un usuario (CA1) y de un servidor (CA2).
func TestDossierFlowWithRealDatabase(t *testing.T) {
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "users", "staff_members", "servers", "server_members", "user_activity", "reports", "moderation_actions", "outbox_events")
	ctx := context.Background()

	users := repository.NewUserRepository(db)
	servers := repository.NewServerRepository(db)
	sync := repository.NewDossierSyncRepository(db)
	activity := repository.NewActivityRepository(db)
	now := time.Now().UTC().Truncate(time.Second)

	for _, u := range []struct{ id, name string }{{flowUser, "Beto"}, {flowOwner, "Dueña"}} {
		if err := users.UpsertRegistration(ctx, repository.Registration{
			UserID: u.id, Name: u.name, Email: u.name + "@example.com", RegisteredAt: now.Add(-72 * time.Hour), EmailVerified: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []struct{ id, name string }{{flowServer, "Sala"}, {flowServer2, "Otra"}} {
		if err := servers.UpsertServer(ctx, repository.ServerSnapshot{
			ServerID: s.id, Name: s.name, OwnerID: flowOwner, CreatedAt: now.Add(-48 * time.Hour), OccurredAt: now.Add(-48 * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Beto entra a las dos salas y se va de la segunda.
	for i, m := range []repository.Membership{
		{EventID: flowID("dddddddd", 1), ServerID: flowServer, UserID: flowUser, IsMember: true, OccurredAt: now.Add(-24 * time.Hour)},
		{EventID: flowID("dddddddd", 2), ServerID: flowServer2, UserID: flowUser, IsMember: true, OccurredAt: now.Add(-23 * time.Hour)},
		{EventID: flowID("dddddddd", 3), ServerID: flowServer2, UserID: flowUser, IsMember: false, OccurredAt: now.Add(-22 * time.Hour)},
	} {
		if err := servers.SetMembership(ctx, m); err != nil {
			t.Fatalf("membresía %d: %v", i, err)
		}
	}
	// Dos mensajes suyos en Sala (hoy) y uno de otra persona.
	for i, m := range []repository.MessageActivity{
		{EventID: flowID("eeeeeeee", 1), UserID: flowUser, ServerID: flowServer, ChannelID: flowID("ffffffff", 1), ChannelName: "general", MessageID: flowID("99999999", 1), OccurredAt: now.Add(-2 * time.Second)},
		{EventID: flowID("eeeeeeee", 2), UserID: flowUser, ServerID: flowServer, ChannelID: flowID("ffffffff", 1), MessageID: flowID("99999999", 2), OccurredAt: now.Add(-time.Second)},
		{EventID: flowID("eeeeeeee", 3), UserID: flowOwner, ServerID: flowServer, ChannelID: flowID("ffffffff", 1), MessageID: flowID("99999999", 3), OccurredAt: now.Add(-time.Second)},
	} {
		if err := activity.InsertMessage(ctx, m); err != nil {
			t.Fatalf("mensaje %d: %v", i, err)
		}
	}
	// Un reporte contra Beto, otro sobre un mensaje suyo, y uno sobre un mensaje desconocido.
	beto := flowUser
	for i, r := range []repository.ReportSnapshot{
		{ReportID: flowID("11111111", 1), ServerID: flowServer, Kind: "user", ReportedUserID: beto, Reason: "insultos", Status: "pending", CreatedAt: now.Add(-3 * time.Hour), OccurredAt: now.Add(-3 * time.Hour)},
		{ReportID: flowID("11111111", 2), ServerID: flowServer, Kind: "message", ChannelID: flowID("ffffffff", 1), MessageID: flowID("99999999", 1), Reason: "spam", Status: "resolved", CreatedAt: now.Add(-2 * time.Hour), ResolvedAt: &now, OccurredAt: now},
		{ReportID: flowID("11111111", 3), ServerID: flowServer, Kind: "message", ChannelID: flowID("ffffffff", 1), MessageID: flowID("99999999", 9), Reason: "otro", Status: "pending", CreatedAt: now.Add(-time.Hour), OccurredAt: now.Add(-time.Hour)},
	} {
		if err := sync.UpsertReport(ctx, r); err != nil {
			t.Fatalf("reporte %d: %v", i, err)
		}
	}
	// Un baneo de un moderador y una suspensión del staff.
	if err := sync.InsertServerAction(ctx, repository.ServerModerationAction{
		ActionID: flowID("22222222", 1), ServerID: flowServer, Kind: "ban", TargetUserID: flowUser, OccurredAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	suspensions := repository.NewSuspensionRepository(db)
	if _, err := suspensions.Suspend(ctx, flowUser, flowActor, now, func(int64) (string, any) { return "admin.user_suspended", map[string]any{} }); err != nil {
		t.Fatal(err)
	}

	router := dossierRouter(service.NewDossierService(repository.NewDossierRepository(db)))
	auth := bearer(t, staffID)

	t.Run("expediente de usuario (CA1)", func(t *testing.T) {
		w := get(router, "/v1/admin/users/"+flowUser+"/dossier", auth)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		var body struct {
			User           map[string]any `json:"user"`
			LastActivityAt string         `json:"last_activity_at"`
			Counts         map[string]int `json:"counts"`
			Latest         struct {
				Servers    []map[string]any `json:"servers"`
				Activity   []map[string]any `json:"activity"`
				Reports    []map[string]any `json:"reports"`
				Moderation []map[string]any `json:"moderation"`
			} `json:"latest"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.User["name"] != "Beto" || body.User["status"] != "suspended" || body.User["suspended_at"] == nil || body.LastActivityAt == "" {
			t.Errorf("usuario = %+v last_activity_at = %q", body.User, body.LastActivityAt)
		}
		// 1 servidor vigente; 2 mensajes + 3 altas/bajas; 2 reportes; baneo + suspensión.
		want := map[string]int{"servers": 1, "activity": 5, "reports": 2, "moderation": 2}
		for k, v := range want {
			if body.Counts[k] != v {
				t.Errorf("counts.%s = %d, quería %d (%s)", k, body.Counts[k], v, w.Body.String())
			}
		}
		if len(body.Latest.Servers) != 1 || body.Latest.Servers[0]["server_name"] != "Sala" {
			t.Errorf("servidores = %+v", body.Latest.Servers)
		}
		if len(body.Latest.Activity) != 5 || body.Latest.Activity[0]["description"] != "Envió un mensaje en un canal de Sala" ||
			body.Latest.Activity[1]["description"] != "Envió un mensaje en #general de Sala" {
			t.Errorf("actividad = %+v", body.Latest.Activity)
		}
		scopes := map[string]string{}
		for _, a := range body.Latest.Moderation {
			scopes[a["kind"].(string)] = a["scope"].(string)
		}
		if scopes["suspension"] != "platform" || scopes["ban"] != "server" {
			t.Errorf("moderación = %+v", body.Latest.Moderation)
		}
		for _, r := range body.Latest.Reports {
			if r["reported_user_id"] != flowUser || r["reported_user_name"] != "Beto" || r["server_name"] != "Sala" {
				t.Errorf("reporte = %+v", r)
			}
		}
	})

	t.Run("sección paginada", func(t *testing.T) {
		w := get(router, "/v1/admin/users/"+flowUser+"/dossier/activity?limit=2&offset=4", auth)
		var page struct {
			Items  []map[string]any `json:"items"`
			Total  int              `json:"total"`
			Limit  int              `json:"limit"`
			Offset int              `json:"offset"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusOK || page.Total != 5 || page.Limit != 2 || page.Offset != 4 || len(page.Items) != 1 {
			t.Errorf("status = %d, página = %+v", w.Code, page)
		}
	})

	t.Run("expediente de servidor (CA2)", func(t *testing.T) {
		w := get(router, "/v1/admin/servers/"+flowServer+"/dossier", auth)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		var body struct {
			Server    map[string]any   `json:"server"`
			Messaging []map[string]any `json:"messaging"`
			Counts    map[string]int   `json:"counts"`
			Latest    struct {
				Reports    []map[string]any `json:"reports"`
				Moderation []map[string]any `json:"moderation"`
			} `json:"latest"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Server["name"] != "Sala" || body.Server["owner_name"] != "Dueña" || body.Server["member_count"] != float64(1) || body.Server["created_at"] == nil {
			t.Errorf("servidor = %+v", body.Server)
		}
		sent := 0.0
		for _, d := range body.Messaging {
			sent += d["messages"].(float64)
		}
		if len(body.Messaging) != 7 || sent != 3 {
			t.Errorf("mensajería = %+v, quería 7 días con 3 mensajes", body.Messaging)
		}
		if body.Counts["reports"] != 3 || body.Counts["moderation"] != 1 {
			t.Errorf("counts = %+v", body.Counts)
		}
		known, unknown := 0, 0
		for _, r := range body.Latest.Reports {
			if r["reported_user_id"] == nil {
				unknown++
			} else {
				known++
			}
		}
		if known != 2 || unknown != 1 {
			t.Errorf("autores conocidos=%d desconocidos=%d, quería 2 y 1 (%+v)", known, unknown, body.Latest.Reports)
		}
	})

	t.Run("no filtra quién reportó, quién actuó ni el contenido", func(t *testing.T) {
		for _, path := range []string{
			"/v1/admin/users/" + flowUser + "/dossier",
			"/v1/admin/users/" + flowUser + "/dossier/reports",
			"/v1/admin/users/" + flowUser + "/dossier/moderation",
			"/v1/admin/servers/" + flowServer + "/dossier",
			"/v1/admin/servers/" + flowServer + "/dossier/moderation",
		} {
			body := get(router, path, auth).Body.String()
			for _, forbidden := range []string{"reporter", "resolved_by", "actor", "content", "channel_id", "message_id", flowActor} {
				if strings.Contains(body, forbidden) {
					t.Errorf("%s: la respuesta contiene %q: %s", path, forbidden, body)
				}
			}
		}
	})

	t.Run("borrar el servidor lo saca del expediente de los usuarios", func(t *testing.T) {
		if err := servers.MarkDeleted(ctx, flowServer, now); err != nil {
			t.Fatal(err)
		}
		if w := get(router, "/v1/admin/servers/"+flowServer+"/dossier", auth); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "SERVER_NOT_FOUND") {
			t.Errorf("servidor borrado: status = %d (%s)", w.Code, w.Body.String())
		}
		w := get(router, "/v1/admin/users/"+flowUser+"/dossier", auth)
		var body struct {
			Counts map[string]int `json:"counts"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		// Quedan: la baja de la sala 2 y su alta (2) y la suspensión de plataforma (1).
		if body.Counts["servers"] != 0 || body.Counts["reports"] != 0 || body.Counts["activity"] != 2 || body.Counts["moderation"] != 1 {
			t.Errorf("counts = %+v", body.Counts)
		}
	})

	t.Run("ids inexistentes", func(t *testing.T) {
		for path, code := range map[string]string{
			"/v1/admin/users/" + flowServer + "/dossier":             "USER_NOT_FOUND",
			"/v1/admin/users/no-es-uuid/dossier":                     "USER_NOT_FOUND",
			"/v1/admin/servers/" + flowUser + "/dossier":             "SERVER_NOT_FOUND",
			"/v1/admin/users/" + flowUser + "/dossier/x":             "INVALID_SECTION",
			"/v1/admin/servers/" + flowServer2 + "/dossier/activity": "INVALID_SECTION",
		} {
			w := get(router, path, auth)
			if !strings.Contains(w.Body.String(), code) {
				t.Errorf("%s: status = %d (%s), quería %s", path, w.Code, w.Body.String(), code)
			}
		}
	})
}
