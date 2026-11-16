package repository_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/admin/internal/testutil"
)

type dossierEnv struct {
	repo     repository.DossierRepository
	servers  repository.ServerRepository
	sync     repository.DossierSyncRepository
	activity repository.ActivityRepository
	db       *sql.DB
}

func newDossierEnv(t *testing.T) dossierEnv {
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "users", "servers", "server_members", "user_activity", "reports", "moderation_actions")
	return dossierEnv{
		repo:     repository.NewDossierRepository(db),
		servers:  repository.NewServerRepository(db),
		sync:     repository.NewDossierSyncRepository(db),
		activity: repository.NewActivityRepository(db),
		db:       db,
	}
}

func (e dossierEnv) join(t *testing.T, serverN, userN, eventN int, at time.Time) {
	t.Helper()
	m := repository.Membership{EventID: uid(2000 + eventN), ServerID: uid(serverN), UserID: uid(userN), IsMember: true, OccurredAt: at}
	if err := e.servers.SetMembership(context.Background(), m); err != nil {
		t.Fatal(err)
	}
}

func (e dossierEnv) platformAction(t *testing.T, n int, kind, userID, serverID string, at time.Time) {
	t.Helper()
	if _, err := e.db.Exec(`INSERT INTO moderation_actions (id, scope, kind, target_user_id, server_id, occurred_at)
		VALUES ($1, 'platform', $2, NULLIF($3, '')::uuid, NULLIF($4, '')::uuid, $5)`, uid(3000+n), kind, userID, serverID, at); err != nil {
		t.Fatal(err)
	}
}

func TestFindUserAndServerOnlySeeWhatTheListingSees(t *testing.T) {
	e := newDossierEnv(t)
	ctx := context.Background()
	seed(t, e.db, []seedUser{
		{n: 1, name: "Ale", email: "ale@example.com", registeredDaysAfter: 0},
		{n: 2, name: "Sin registro", email: "", registeredDaysAfter: -1},
	})
	seedServer(t, e.servers, 1, "Sala")
	if err := e.servers.MarkDeleted(ctx, uid(2), base); err != nil { // solo existe como borrado
		t.Fatal(err)
	}

	if u, err := e.repo.FindUser(ctx, uid(1)); err != nil || u.Name != "Ale" || u.Status != model.UserStatusActive || !u.CreatedAt.Equal(base) {
		t.Errorf("usuario = %+v, err = %v", u, err)
	}
	for _, n := range []int{2, 3} {
		if _, err := e.repo.FindUser(ctx, uid(n)); !errors.Is(err, repository.ErrUserNotFound) {
			t.Errorf("usuario %d: err = %v, quería ErrUserNotFound", n, err)
		}
	}
	if s, err := e.repo.FindServer(ctx, uid(1)); err != nil || s.Name != "Sala" {
		t.Errorf("servidor = %+v, err = %v", s, err)
	}
	for _, n := range []int{2, 3} {
		if _, err := e.repo.FindServer(ctx, uid(n)); !errors.Is(err, repository.ErrServerNotFound) {
			t.Errorf("servidor %d: err = %v, quería ErrServerNotFound", n, err)
		}
	}
}

func TestUserMembershipsListsOnlyCurrentServersNewestFirst(t *testing.T) {
	e := newDossierEnv(t)
	ctx := context.Background()
	seedServer(t, e.servers, 1, "Uno")
	seedServer(t, e.servers, 2, "Dos")
	seedServer(t, e.servers, 3, "Tres")
	e.join(t, 1, 7, 1, base)
	e.join(t, 2, 7, 2, base.Add(time.Hour))
	e.join(t, 3, 7, 3, base.Add(2*time.Hour))
	if err := e.servers.SetMembership(ctx, repository.Membership{ServerID: uid(3), UserID: uid(7), IsMember: false, OccurredAt: base.Add(3 * time.Hour)}); err != nil {
		t.Fatal(err) // se fue del 3
	}
	e.join(t, 1, 8, 4, base) // otro usuario

	got, total, err := e.repo.UserMemberships(ctx, uid(7), 10, 0)
	if err != nil || total != 2 || len(got) != 2 {
		t.Fatalf("servidores = %+v, total = %d, err = %v", got, total, err)
	}
	if got[0].ServerID != uid(2) || *got[0].ServerName != "Dos" || !got[0].JoinedAt.Equal(base.Add(time.Hour)) || got[1].ServerID != uid(1) {
		t.Errorf("servidores = %+v", got)
	}
	if got[0].ServerIconURL != nil {
		t.Errorf("un servidor sin ícono debe venir con null, vino %q", *got[0].ServerIconURL)
	}
	withIcon := snap(2, "Dos", 9, 0, 10)
	withIcon.IconURL = "/v1/servers/" + uid(2) + "/icon?v=abc"
	if err := e.servers.UpsertServer(ctx, withIcon); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := e.repo.UserMemberships(ctx, uid(7), 10, 0); got[0].ServerIconURL == nil || *got[0].ServerIconURL != withIcon.IconURL {
		t.Errorf("ícono del servidor = %v, quería %q", got[0].ServerIconURL, withIcon.IconURL)
	}

	// Una página más allá del final trae el total y ningún ítem.
	got, total, err = e.repo.UserMemberships(ctx, uid(7), 10, 5)
	if err != nil || total != 2 || len(got) != 0 || got == nil {
		t.Errorf("página vacía = %+v, total = %d, err = %v", got, total, err)
	}

	// Un servidor borrado desaparece del expediente.
	if err := e.servers.MarkDeleted(ctx, uid(1), base.Add(4*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, total, _ := e.repo.UserMemberships(ctx, uid(7), 10, 0); total != 1 {
		t.Errorf("total = %d tras borrar un servidor", total)
	}
}

func TestUserActivityNewestFirstWithNamesAndLastActivity(t *testing.T) {
	e := newDossierEnv(t)
	ctx := context.Background()
	seedServer(t, e.servers, 1, "Sala")
	e.join(t, 1, 7, 1, base)
	if err := e.activity.InsertMessage(ctx, message(1, base.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	noName := message(2, base.Add(2*time.Hour))
	noName.ChannelName = ""
	if err := e.activity.InsertMessage(ctx, noName); err != nil {
		t.Fatal(err)
	}

	got, total, err := e.repo.UserActivity(ctx, uid(7), 10, 0)
	if err != nil || total != 3 || len(got) != 3 {
		t.Fatalf("actividad = %+v, total = %d, err = %v", got, total, err)
	}
	if got[0].Kind != "message_sent" || got[0].ChannelName != "" || got[1].ChannelName != "general" || got[1].ServerName != "Sala" ||
		got[2].Kind != "joined_server" {
		t.Errorf("actividad = %+v", got)
	}

	last, err := e.repo.UserLastActivity(ctx, uid(7))
	if err != nil || last == nil || !last.Equal(base.Add(2*time.Hour)) {
		t.Errorf("última actividad = %v, err = %v", last, err)
	}
	if none, err := e.repo.UserLastActivity(ctx, uid(99)); err != nil || none != nil {
		t.Errorf("sin actividad = %v, err = %v", none, err)
	}
}

func TestReportsOfAUserIncludeThoseOfTheirMessagesAndResolveTheAuthorWhenReading(t *testing.T) {
	e := newDossierEnv(t)
	ctx := context.Background()
	seed(t, e.db, []seedUser{{n: 7, name: "Beto", email: "beto@example.com", registeredDaysAfter: 0}})
	seedServer(t, e.servers, 1, "Sala")

	// Reporte contra el usuario.
	userReport := report(1, "pending", base.Add(time.Hour))
	userReport.CreatedAt = base.Add(time.Hour)
	// Reporte de un mensaje suyo: el reporte llega ANTES que message_created.
	msgReport := report(2, "resolved", base.Add(3*time.Hour))
	msgReport.Kind, msgReport.ReportedUserID, msgReport.ChannelID, msgReport.MessageID = "message", "", uid(20), uid(901)
	msgReport.CreatedAt = base.Add(2 * time.Hour)
	// Reporte de un mensaje de alguien desconocido.
	strangerReport := report(3, "pending", base.Add(4*time.Hour))
	strangerReport.Kind, strangerReport.ReportedUserID, strangerReport.ChannelID, strangerReport.MessageID = "message", "", uid(20), uid(999)
	strangerReport.CreatedAt = base.Add(3 * time.Hour)
	for _, r := range []repository.ReportSnapshot{userReport, msgReport, strangerReport} {
		if err := e.sync.UpsertReport(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	// Todavía no se sabe quién escribió el mensaje 901.
	got, total, err := e.repo.UserReports(ctx, uid(7), 10, 0)
	if err != nil || total != 1 || got[0].ID != uid(501) {
		t.Fatalf("antes de message_created: reportes = %+v, total = %d, err = %v", got, total, err)
	}

	// Llega message_created: el reporte se atribuye solo, sin reprocesarlo.
	if err := e.activity.InsertMessage(ctx, message(1, base.Add(30*time.Minute))); err != nil {
		t.Fatal(err)
	}
	got, total, err = e.repo.UserReports(ctx, uid(7), 10, 0)
	if err != nil || total != 2 || len(got) != 2 {
		t.Fatalf("después de message_created: reportes = %+v, total = %d, err = %v", got, total, err)
	}
	m := got[0] // el más nuevo primero
	if m.ID != uid(502) || m.Kind != "message" || m.Status != "resolved" || m.ResolvedAt == nil || m.ServerID != uid(1) ||
		*m.ServerName != "Sala" || *m.ReportedUserID != uid(7) || *m.ReportedUserName != "Beto" {
		t.Errorf("reporte de mensaje = %+v", m)
	}
	u := got[1]
	if u.ID != uid(501) || u.Kind != "user" || u.Status != "pending" || u.ResolvedAt != nil || *u.ReportedUserID != uid(7) {
		t.Errorf("reporte de usuario = %+v", u)
	}

	// En el expediente del servidor aparecen los tres, con autor null cuando no se conoce.
	all, total, err := e.repo.ServerReports(ctx, uid(1), 10, 0)
	if err != nil || total != 3 || len(all) != 3 {
		t.Fatalf("reportes del servidor = %+v, total = %d, err = %v", all, total, err)
	}
	if all[0].ID != uid(503) || all[0].ReportedUserID != nil || all[0].ReportedUserName != nil {
		t.Errorf("reporte sin autor conocido = %+v", all[0])
	}
	if all[1].ID != uid(502) || *all[1].ReportedUserID != uid(7) {
		t.Errorf("reporte con autor = %+v", all[1])
	}
}

func TestModerationOfAUserAndOfAServerMixPlatformAndServerScopes(t *testing.T) {
	e := newDossierEnv(t)
	ctx := context.Background()
	seed(t, e.db, []seedUser{{n: 7, name: "Beto", email: "beto@example.com", registeredDaysAfter: 0}})
	seedServer(t, e.servers, 1, "Sala")

	e.platformAction(t, 1, "suspension", uid(7), "", base.Add(3*time.Hour)) // al usuario
	e.platformAction(t, 2, "suspension", "", uid(1), base.Add(4*time.Hour)) // al servidor
	e.platformAction(t, 3, "deletion_requested", "", uid(1), base.Add(5*time.Hour))
	if err := e.sync.InsertServerAction(ctx, serverAction(1, "ban")); err != nil { // moderador, sobre el usuario 7
		t.Fatal(err)
	}

	byUser, total, err := e.repo.UserModeration(ctx, uid(7), 10, 0)
	if err != nil || total != 2 || len(byUser) != 2 {
		t.Fatalf("moderación del usuario = %+v, total = %d, err = %v", byUser, total, err)
	}
	if a := byUser[0]; a.Kind != "suspension" || a.Scope != "platform" || a.ServerID != nil || *a.TargetUserID != uid(7) || *a.TargetUserName != "Beto" {
		t.Errorf("suspensión = %+v", a)
	}
	if a := byUser[1]; a.Kind != "ban" || a.Scope != "server" || *a.ServerID != uid(1) || *a.ServerName != "Sala" {
		t.Errorf("baneo = %+v", a)
	}

	byServer, total, err := e.repo.ServerModeration(ctx, uid(1), 10, 0)
	if err != nil || total != 3 || len(byServer) != 3 {
		t.Fatalf("moderación del servidor = %+v, total = %d, err = %v", byServer, total, err)
	}
	if byServer[0].Kind != "deletion_requested" || byServer[1].Kind != "suspension" || byServer[1].TargetUserID != nil ||
		byServer[2].Kind != "ban" || *byServer[2].TargetUserName != "Beto" {
		t.Errorf("moderación del servidor = %+v", byServer)
	}
	if page, _, _ := e.repo.ServerModeration(ctx, uid(1), 1, 1); len(page) != 1 || page[0].Kind != "suspension" {
		t.Errorf("paginación = %+v", page)
	}
}

func TestServerMessagingHasSevenUTCDaysWithZeros(t *testing.T) {
	e := newDossierEnv(t)
	ctx := context.Background()
	today := time.Date(2026, 11, 8, 15, 0, 0, 0, time.UTC)
	at := func(day, hour, min int) time.Time { return time.Date(2026, 11, day, hour, min, 0, 0, time.UTC) }

	n := 0
	send := func(serverN int, when time.Time) {
		n++
		m := message(n, when)
		m.ServerID = uid(serverN)
		if err := e.activity.InsertMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	send(1, at(8, 0, 0))   // hoy, apenas empezó el día
	send(1, at(8, 14, 0))  // hoy
	send(1, at(5, 23, 59)) // el 5, último minuto
	send(1, at(6, 0, 0))   // el 6, primer instante
	send(1, at(1, 12, 0))  // fuera de la ventana (hace 7 días)
	send(1, at(9, 1, 0))   // mañana: fuera de la ventana
	send(2, at(8, 10, 0))  // otro servidor

	got, err := e.repo.ServerMessaging(ctx, uid(1), today)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.MessagingDay{
		{Date: "2026-11-02", Messages: 0}, {Date: "2026-11-03", Messages: 0}, {Date: "2026-11-04", Messages: 0},
		{Date: "2026-11-05", Messages: 1}, {Date: "2026-11-06", Messages: 1}, {Date: "2026-11-07", Messages: 0},
		{Date: "2026-11-08", Messages: 2},
	}
	if len(got) != len(want) {
		t.Fatalf("días = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("día %d = %+v, quería %+v", i, got[i], want[i])
		}
	}

	// Un servidor sin actividad también devuelve los 7 días, todos en cero.
	empty, err := e.repo.ServerMessaging(ctx, uid(3), today)
	if err != nil || len(empty) != 7 {
		t.Fatalf("días vacíos = %+v, err = %v", empty, err)
	}
	for _, d := range empty {
		if d.Messages != 0 {
			t.Errorf("día %s = %d, quería 0", d.Date, d.Messages)
		}
	}
}

func TestFindUserAndServerIncludeTheirImages(t *testing.T) {
	e := newDossierEnv(t)
	ctx := context.Background()
	seed(t, e.db, []seedUser{{n: 1, name: "Ale", email: "ale@example.com"}})
	if err := repository.NewUserRepository(e.db).UpsertProfile(ctx, repository.Profile{UserID: uid(1), Name: "Ale", AvatarURL: "/uploads/profile-images/ale.png", Version: 1}); err != nil {
		t.Fatal(err)
	}
	s := snap(1, "Sala", 1, 0, 1)
	s.IconURL = "/v1/servers/" + uid(1) + "/icon?v=abc"
	if err := e.servers.UpsertServer(ctx, s); err != nil {
		t.Fatal(err)
	}

	if u, err := e.repo.FindUser(ctx, uid(1)); err != nil || u.AvatarURL == nil || *u.AvatarURL != "/uploads/profile-images/ale.png" {
		t.Errorf("usuario = %+v, err = %v", u, err)
	}
	if srv, err := e.repo.FindServer(ctx, uid(1)); err != nil || srv.IconURL == nil || *srv.IconURL != s.IconURL {
		t.Errorf("servidor = %+v, err = %v", srv, err)
	}
}
