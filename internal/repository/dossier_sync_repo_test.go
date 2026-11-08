package repository_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/admin/internal/testutil"
)

func newDossierSync(t *testing.T) (repository.DossierSyncRepository, repository.ServerRepository, *sql.DB) {
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "servers", "server_members", "reports", "moderation_actions")
	return repository.NewDossierSyncRepository(db), repository.NewServerRepository(db), db
}

func report(n int, status string, at time.Time) repository.ReportSnapshot {
	r := repository.ReportSnapshot{
		ReportID: uid(500 + n), ServerID: uid(1), Kind: "user", ReportedUserID: uid(7),
		Reason: "insultos", Status: status, CreatedAt: base, OccurredAt: at,
	}
	if status != "pending" {
		r.ResolvedAt = &at
	}
	return r
}

type reportRow struct {
	status, kind string
	reportedUser sql.NullString
	channel      sql.NullString
	message      sql.NullString
	resolvedAt   sql.NullTime
	snapshotAt   time.Time
}

func reportRows(t *testing.T, db *sql.DB) map[string]reportRow {
	t.Helper()
	rows, err := db.Query(`SELECT report_id::text, status, kind, reported_user_id::text, channel_id::text, message_id::text, resolved_at, snapshot_at FROM reports`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]reportRow{}
	for rows.Next() {
		var id string
		var r reportRow
		if err := rows.Scan(&id, &r.status, &r.kind, &r.reportedUser, &r.channel, &r.message, &r.resolvedAt, &r.snapshotAt); err != nil {
			t.Fatal(err)
		}
		out[id] = r
	}
	return out
}

func TestUpsertReportKeepsTheNewestSnapshot(t *testing.T) {
	repo, _, db := newDossierSync(t)
	ctx := context.Background()
	t1, t2 := base.Add(time.Hour), base.Add(2*time.Hour)

	// Llega primero la foto nueva (resuelto) y después la vieja (pending): gana la nueva.
	if err := repo.UpsertReport(ctx, report(1, "resolved", t2)); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertReport(ctx, report(1, "pending", t1)); err != nil {
		t.Fatal(err)
	}
	got := reportRows(t, db)
	if r := got[uid(501)]; len(got) != 1 || r.status != "resolved" || !r.resolvedAt.Valid || !r.snapshotAt.Equal(t2) {
		t.Fatalf("reportes = %+v", got)
	}

	// Una foto más nueva la pisa; repetir la misma no cambia nada.
	for i := 0; i < 2; i++ {
		if err := repo.UpsertReport(ctx, report(1, "dismissed", base.Add(3*time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	if got := reportRows(t, db); len(got) != 1 || got[uid(501)].status != "dismissed" {
		t.Errorf("reportes = %+v", got)
	}
}

func TestUpsertReportStoresNullsForTheMissingIds(t *testing.T) {
	repo, _, db := newDossierSync(t)
	r := report(1, "pending", base)
	r.Kind, r.ReportedUserID, r.ChannelID, r.MessageID = "message", "", uid(20), uid(30)
	if err := repo.UpsertReport(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	got := reportRows(t, db)[uid(501)]
	if got.reportedUser.Valid || got.channel.String != uid(20) || got.message.String != uid(30) || got.resolvedAt.Valid {
		t.Errorf("fila = %+v", got)
	}
}

func TestReportOfAnUnknownServerIsStoredAndOneOfADeletedServerIsDiscarded(t *testing.T) {
	repo, servers, db := newDossierSync(t)
	ctx := context.Background()

	// admin todavía no conoce el servidor 1: se guarda igual.
	if err := repo.UpsertReport(ctx, report(1, "pending", base)); err != nil {
		t.Fatal(err)
	}
	if got := reportRows(t, db); len(got) != 1 {
		t.Fatalf("reportes = %+v", got)
	}
	// Borrado el servidor, un report_updated tardío no resucita nada.
	if err := servers.MarkDeleted(ctx, uid(1), base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := reportRows(t, db); len(got) != 0 {
		t.Fatalf("server_deleted tenía que purgar los reportes: %+v", got)
	}
	if err := repo.UpsertReport(ctx, report(2, "pending", base.Add(2*time.Hour))); err != nil {
		t.Fatal(err)
	}
	if got := reportRows(t, db); len(got) != 0 {
		t.Errorf("un reporte tardío no debió guardarse: %+v", got)
	}
}

func serverAction(n int, kind string) repository.ServerModerationAction {
	return repository.ServerModerationAction{
		ActionID: uid(600 + n), ServerID: uid(1), Kind: kind, TargetUserID: uid(7), OccurredAt: base.Add(time.Duration(n) * time.Minute),
	}
}

func TestInsertServerActionIsIdempotent(t *testing.T) {
	repo, _, db := newDossierSync(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := repo.InsertServerAction(ctx, serverAction(1, "ban")); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.InsertServerAction(ctx, serverAction(2, "unban")); err != nil {
		t.Fatal(err)
	}
	got := historyOf(t, db)
	if len(got) != 2 || got[0].kind != "ban" || got[0].scope != "server" || got[0].targetUser.String != uid(7) ||
		got[0].server.String != uid(1) || got[0].actor.Valid || got[1].kind != "unban" {
		t.Errorf("historial = %+v", got)
	}
}

func TestServerDeletionPurgesServerScopeButKeepsPlatformActions(t *testing.T) {
	repo, servers, db := newDossierSync(t)
	ctx := context.Background()
	if err := repo.InsertServerAction(ctx, serverAction(1, "ban")); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertReport(ctx, report(1, "pending", base)); err != nil {
		t.Fatal(err)
	}
	// Un reporte de otro servidor y una acción de plataforma sobre el servidor 1.
	other := report(2, "pending", base)
	other.ServerID = uid(2)
	if err := repo.UpsertReport(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO moderation_actions (id, scope, kind, server_id, occurred_at)
		VALUES ($1, 'platform', 'suspension', $2, $3)`, uid(700), uid(1), base); err != nil {
		t.Fatal(err)
	}

	// Repetir server_deleted tampoco rompe nada.
	for i := 0; i < 2; i++ {
		if err := servers.MarkDeleted(ctx, uid(1), base.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if got := reportRows(t, db); len(got) != 1 || got[uid(502)].status != "pending" {
		t.Errorf("reportes = %+v, quería solo el del otro servidor", got)
	}
	got := historyOf(t, db)
	if len(got) != 1 || got[0].scope != "platform" || got[0].kind != "suspension" {
		t.Errorf("historial = %+v, quería solo la acción de plataforma", got)
	}

	// Una acción tardía de un servidor borrado se descarta.
	if err := repo.InsertServerAction(ctx, serverAction(3, "kick")); err != nil {
		t.Fatal(err)
	}
	if got := historyOf(t, db); len(got) != 1 {
		t.Errorf("historial = %+v, la acción tardía no debió guardarse", got)
	}
}
