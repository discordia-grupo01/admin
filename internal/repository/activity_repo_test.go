package repository_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/admin/internal/testutil"
)

func newActivity(t *testing.T) (repository.ActivityRepository, repository.ServerRepository, *sql.DB) {
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "servers", "server_members", "user_activity", "reports", "moderation_actions", "consumed_events")
	return repository.NewActivityRepository(db), repository.NewServerRepository(db), db
}

type activityRow struct {
	id, userID, kind, serverID string
	channelName, messageID     sql.NullString
	occurredAt                 time.Time
}

func activityOf(t *testing.T, db *sql.DB) []activityRow {
	t.Helper()
	rows, err := db.Query(`SELECT id::text, user_id::text, kind, server_id::text, channel_name, message_id::text, occurred_at
		FROM user_activity ORDER BY occurred_at, id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []activityRow
	for rows.Next() {
		var r activityRow
		if err := rows.Scan(&r.id, &r.userID, &r.kind, &r.serverID, &r.channelName, &r.messageID, &r.occurredAt); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func message(n int, at time.Time) repository.MessageActivity {
	return repository.MessageActivity{
		EventID: uid(800 + n), UserID: uid(7), ServerID: uid(1), ChannelID: uid(20), ChannelName: "general",
		MessageID: uid(900 + n), OccurredAt: at,
	}
}

func TestInsertMessageStoresWhereAndWhenAndIsIdempotent(t *testing.T) {
	repo, _, db := newActivity(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := repo.InsertMessage(ctx, message(1, base)); err != nil {
			t.Fatal(err)
		}
	}
	noName := message(2, base.Add(time.Minute))
	noName.ChannelName = ""
	if err := repo.InsertMessage(ctx, noName); err != nil {
		t.Fatal(err)
	}

	got := activityOf(t, db)
	if len(got) != 2 {
		t.Fatalf("actividad = %+v, el evento repetido no debió duplicarse", got)
	}
	if r := got[0]; r.id != uid(801) || r.userID != uid(7) || r.kind != "message_sent" || r.serverID != uid(1) ||
		r.channelName.String != "general" || r.messageID.String != uid(901) || !r.occurredAt.Equal(base) {
		t.Errorf("fila = %+v", r)
	}
	if got[1].channelName.Valid {
		t.Errorf("un channel_name vacío tiene que quedar NULL: %+v", got[1])
	}
}

func TestMessageOfADeletedServerIsDiscardedAndDeletionPurgesTheActivity(t *testing.T) {
	repo, servers, db := newActivity(t)
	ctx := context.Background()
	if err := repo.InsertMessage(ctx, message(1, base)); err != nil {
		t.Fatal(err)
	}
	other := message(2, base)
	other.ServerID = uid(2)
	if err := repo.InsertMessage(ctx, other); err != nil {
		t.Fatal(err)
	}

	if err := servers.MarkDeleted(ctx, uid(1), base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := activityOf(t, db); len(got) != 1 || got[0].serverID != uid(2) {
		t.Fatalf("actividad = %+v, quería solo la del otro servidor", got)
	}
	if err := repo.InsertMessage(ctx, message(3, base.Add(2*time.Hour))); err != nil {
		t.Fatal(err)
	}
	if got := activityOf(t, db); len(got) != 1 {
		t.Errorf("actividad = %+v, un mensaje tardío no debe resucitar el servidor", got)
	}
}

func TestPruneDeletesOnlyWhatIsOlderThanTheCutoff(t *testing.T) {
	repo, _, db := newActivity(t)
	ctx := context.Background()
	cutoff := base.Add(24 * time.Hour)
	for i, at := range []time.Time{cutoff.Add(-time.Second), cutoff, cutoff.Add(time.Hour)} {
		if err := repo.InsertMessage(ctx, message(i+1, at)); err != nil {
			t.Fatal(err)
		}
	}
	n, err := repo.Prune(ctx, cutoff)
	if err != nil || n != 1 {
		t.Fatalf("borradas = %d, err = %v, quería 1", n, err)
	}
	if got := activityOf(t, db); len(got) != 2 || !got[0].occurredAt.Equal(cutoff) {
		t.Errorf("actividad = %+v", got)
	}
}

func joined(eventN int, userN int, isMember bool, at time.Time) repository.Membership {
	return repository.Membership{EventID: uid(1000 + eventN), ServerID: uid(1), UserID: uid(userN), IsMember: isMember, OccurredAt: at}
}

func TestMembershipRecordsActivityInTheSameCallAndIsIdempotent(t *testing.T) {
	_, servers, db := newActivity(t)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if err := servers.SetMembership(ctx, joined(1, 7, true, base)); err != nil {
			t.Fatal(err)
		}
	}
	if err := servers.SetMembership(ctx, joined(2, 7, false, base.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	// Un alta anterior a la baja que llega tarde: la membresía no revive, pero el hecho se registra.
	if err := servers.SetMembership(ctx, joined(0, 7, true, base.Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}

	got := activityOf(t, db)
	if len(got) != 3 {
		t.Fatalf("actividad = %+v, quería 3 hechos distintos", got)
	}
	kinds := []string{got[0].kind, got[1].kind, got[2].kind}
	if kinds[0] != "joined_server" || kinds[1] != "joined_server" || kinds[2] != "left_server" {
		t.Errorf("kinds = %v", kinds)
	}
	var isMember bool
	if err := db.QueryRow(`SELECT is_member FROM server_members WHERE user_id = $1`, uid(7)).Scan(&isMember); err != nil || isMember {
		t.Errorf("is_member = %v, err = %v: el alta vieja no debe revivir la membresía", isMember, err)
	}
}

func TestMembershipOfADeletedServerLeavesNoActivity(t *testing.T) {
	_, servers, db := newActivity(t)
	ctx := context.Background()
	if err := servers.MarkDeleted(ctx, uid(1), base); err != nil {
		t.Fatal(err)
	}
	// Los member_left que publica el propio borrado llegan después de server_deleted.
	if err := servers.SetMembership(ctx, joined(1, 7, false, base.Add(time.Second))); err != nil {
		t.Fatal(err)
	}
	if got := activityOf(t, db); len(got) != 0 {
		t.Errorf("actividad = %+v, no debió quedar nada", got)
	}
}

func TestMembershipWithoutAUUIDEventIDStillAppliesButRecordsNoActivity(t *testing.T) {
	_, servers, db := newActivity(t)
	m := joined(1, 7, true, base)
	m.EventID = "evt-1"
	if err := servers.SetMembership(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM server_members WHERE is_member`).Scan(&n); err != nil || n != 1 {
		t.Errorf("miembros = %d, err = %v", n, err)
	}
	if got := activityOf(t, db); len(got) != 0 {
		t.Errorf("actividad = %+v", got)
	}
}

// La migración 000014 reconstruye altas y bajas desde consumed_events.
func TestBackfillMembershipActivityRebuildsFromConsumedEventsAndIsIdempotent(t *testing.T) {
	_, servers, db := newActivity(t)
	sqlText, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000014_backfill_membership_activity.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	seedEvent := func(n int, key, status, serverN string, body string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO consumed_events (id, event_id, routing_key, raw_body, status)
			VALUES (gen_random_uuid(), $1, $2, $3, $4)`, uid(1100+n), key, body, status); err != nil {
			t.Fatal(err)
		}
	}
	bodyOf := func(n int, serverN int) string {
		return `{"event_id":"` + uid(1100+n) + `","occurred_at":"2026-11-0` + string(rune('0'+n)) + `T10:00:00Z","server_id":"` + uid(serverN) + `","user_id":"` + uid(7) + `"}`
	}
	seedEvent(1, "servers.member_joined", "processed", "", bodyOf(1, 1))
	seedEvent(2, "servers.member_left", "processed", "", bodyOf(2, 1))
	seedEvent(3, "servers.member_joined", "processed", "", bodyOf(3, 2)) // servidor borrado
	seedEvent(4, "servers.member_joined", "pending", "", bodyOf(4, 1))   // no se procesó
	seedEvent(5, "servers.server_updated", "processed", "", bodyOf(5, 1))
	if err := servers.MarkDeleted(context.Background(), uid(2), base); err != nil {
		t.Fatal(err)
	}

	for run := 1; run <= 2; run++ {
		if _, err := db.Exec(string(sqlText)); err != nil {
			t.Fatalf("corrida %d: %v", run, err)
		}
		got := activityOf(t, db)
		if len(got) != 2 || got[0].kind != "joined_server" || got[1].kind != "left_server" ||
			got[0].id != uid(1101) || got[0].serverID != uid(1) || got[0].userID != uid(7) {
			t.Fatalf("corrida %d: actividad = %+v", run, got)
		}
	}
}
