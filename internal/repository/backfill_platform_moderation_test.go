package repository_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/discordia-grupo01/admin/internal/testutil"
)

// La migración 000011 reconstruye el historial desde el outbox. Se corre acá su
// SQL sobre eventos sembrados, dos veces, para fijar que no duplica filas.
func TestBackfillPlatformModerationRebuildsHistoryFromTheOutboxAndIsIdempotent(t *testing.T) {
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "outbox_events", "moderation_actions")

	sql, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000011_backfill_platform_moderation.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	seedEvent := func(id, key, field, value, at string) {
		t.Helper()
		if _, err := db.Exec(`
			INSERT INTO outbox_events (id, routing_key, payload)
			VALUES (gen_random_uuid(), $1, jsonb_build_object('event_id', $2::text, 'occurred_at', $3::text, $4::text, $5::text))`,
			key, id, at, field, value); err != nil {
			t.Fatal(err)
		}
	}
	seedEvent(uid(1001), "admin.user_suspended", "user_id", uid(1), "2026-11-01T10:00:00Z")
	seedEvent(uid(1002), "admin.user_reactivated", "user_id", uid(1), "2026-11-02T10:00:00Z")
	seedEvent(uid(1003), "admin.server_suspended", "server_id", uid(7), "2026-11-03T10:00:00Z")
	seedEvent(uid(1004), "admin.server_reactivated", "server_id", uid(7), "2026-11-04T10:00:00Z")
	seedEvent(uid(1005), "admin.server_deletion_requested", "server_id", uid(7), "2026-11-05T10:00:00Z")
	// Un evento que no es de moderación no entra.
	seedEvent(uid(1006), "admin.staff_member_added", "user_id", uid(2), "2026-11-05T11:00:00Z")

	for run := 1; run <= 2; run++ {
		if _, err := db.Exec(string(sql)); err != nil {
			t.Fatalf("corrida %d: %v", run, err)
		}
		got := historyOf(t, db)
		if len(got) != 5 {
			t.Fatalf("corrida %d: historial = %+v, quería 5 filas", run, got)
		}
	}

	want := []struct{ kind, user, server string }{
		{"suspension", uid(1), ""},
		{"reactivation", uid(1), ""},
		{"suspension", "", uid(7)},
		{"reactivation", "", uid(7)},
		{"deletion_requested", "", uid(7)},
	}
	for i, r := range historyOf(t, db) {
		if r.kind != want[i].kind || r.scope != "platform" || r.targetUser.String != want[i].user ||
			r.server.String != want[i].server || r.actor.Valid {
			t.Errorf("fila %d = %+v, quería %+v", i, r, want[i])
		}
	}
	var id string
	if err := db.QueryRow(`SELECT id::text FROM moderation_actions WHERE kind = 'deletion_requested'`).Scan(&id); err != nil || id != uid(1005) {
		t.Errorf("el id tiene que ser el event_id: %q, %v", id, err)
	}
}
