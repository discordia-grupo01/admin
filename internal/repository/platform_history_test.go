package repository_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/admin/internal/testutil"
)

type historyRow struct {
	kind, scope        string
	targetUser, server sql.NullString
	actor              sql.NullString
	occurredAt         time.Time
}

func historyOf(t *testing.T, db *sql.DB) []historyRow {
	t.Helper()
	rows, err := db.Query(`
		SELECT kind, scope, target_user_id::text, server_id::text, actor_id::text, occurred_at
		  FROM moderation_actions ORDER BY occurred_at, kind`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []historyRow
	for rows.Next() {
		var r historyRow
		if err := rows.Scan(&r.kind, &r.scope, &r.targetUser, &r.server, &r.actor, &r.occurredAt); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestUserSuspensionAndReactivationAreRecordedInTheHistory(t *testing.T) {
	repo, db := newSuspensionRepo(t)
	testutil.Truncate(t, db, "moderation_actions")
	seed(t, db, []seedUser{{n: 1, name: "Ale", email: "ale@example.com", registeredDaysAfter: 0}})

	if _, err := repo.Suspend(context.Background(), uid(1), uid(99), suspendedAt, eventFor("admin.user_suspended")); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Reactivate(context.Background(), uid(1), eventFor("admin.user_reactivated")); err != nil {
		t.Fatal(err)
	}

	got := historyOf(t, db)
	if len(got) != 2 {
		t.Fatalf("historial = %+v, quería 2 filas", got)
	}
	s, r := got[0], got[1]
	if s.kind != "suspension" || s.scope != "platform" || s.targetUser.String != uid(1) || s.server.Valid ||
		s.actor.String != uid(99) || !s.occurredAt.Equal(suspendedAt) {
		t.Errorf("suspensión: %+v", s)
	}
	if r.kind != "reactivation" || r.scope != "platform" || r.targetUser.String != uid(1) || r.server.Valid || r.actor.Valid {
		t.Errorf("reactivación: %+v", r)
	}
}

func TestAFailedUserActionLeavesNothingInTheHistory(t *testing.T) {
	repo, db := newSuspensionRepo(t)
	testutil.Truncate(t, db, "moderation_actions")
	seed(t, db, []seedUser{{n: 1, name: "Ale", email: "ale@example.com", registeredDaysAfter: 0}})
	ctx := context.Background()

	// Reactivar a alguien que no está suspendido: 409, nada en el historial.
	if _, err := repo.Reactivate(ctx, uid(1), eventFor("admin.user_reactivated")); !errors.Is(err, repository.ErrNotSuspended) {
		t.Fatalf("err = %v", err)
	}
	// Suspender dos veces: la segunda falla y no suma otra fila.
	if _, err := repo.Suspend(ctx, uid(1), uid(99), suspendedAt, eventFor("admin.user_suspended")); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Suspend(ctx, uid(1), uid(99), suspendedAt, eventFor("admin.user_suspended")); !errors.Is(err, repository.ErrAlreadySuspended) {
		t.Fatalf("err = %v", err)
	}
	if got := historyOf(t, db); len(got) != 1 {
		t.Errorf("historial = %+v, quería solo la primera suspensión", got)
	}
}

func TestUserSuspensionRolledBackForAnUnwritableEventLeavesNoHistory(t *testing.T) {
	repo, db := newSuspensionRepo(t)
	testutil.Truncate(t, db, "moderation_actions")
	seed(t, db, []seedUser{{n: 1, name: "Ale", email: "ale@example.com", registeredDaysAfter: 0}})

	unserializable := func(int64) (string, any) { return "k", make(chan int) }
	if _, err := repo.Suspend(context.Background(), uid(1), uid(99), suspendedAt, unserializable); err == nil {
		t.Fatal("tenía que fallar")
	}
	if got := historyOf(t, db); len(got) != 0 {
		t.Errorf("historial = %+v, no debió quedar nada", got)
	}
}

func TestServerActionsAreRecordedInTheHistory(t *testing.T) {
	repo, servers, db := newModerationRepo(t)
	testutil.Truncate(t, db, "moderation_actions")
	seedServer(t, servers, 1, "Sala")
	ctx := context.Background()

	suspend(t, repo, 1)
	if _, err := repo.Reactivate(ctx, uid(1), eventFor("admin.server_reactivated")); err != nil {
		t.Fatal(err)
	}
	if err := repo.RequestDeletion(ctx, uid(1), "Sala", uid(99), requestedAt, eventFor("admin.server_deletion_requested")); err != nil {
		t.Fatal(err)
	}

	got := historyOf(t, db)
	kinds := map[string]historyRow{}
	for _, r := range got {
		kinds[r.kind+"/"+r.scope] = r
		if r.server.String != uid(1) || r.targetUser.Valid {
			t.Errorf("la acción es sobre el servidor, no sobre un usuario: %+v", r)
		}
	}
	if len(got) != 3 {
		t.Fatalf("historial = %+v, quería 3 filas", got)
	}
	if r := kinds["suspension/platform"]; !r.occurredAt.Equal(serverSuspendedAt) || r.actor.String != uid(99) {
		t.Errorf("suspensión: %+v", r)
	}
	if r := kinds["deletion_requested/platform"]; !r.occurredAt.Equal(requestedAt) || r.actor.String != uid(99) {
		t.Errorf("pedido de eliminación: %+v", r)
	}
	if _, ok := kinds["reactivation/platform"]; !ok {
		t.Errorf("falta la reactivación: %+v", got)
	}
}

func TestAFailedServerActionLeavesNothingInTheHistory(t *testing.T) {
	repo, servers, db := newModerationRepo(t)
	testutil.Truncate(t, db, "moderation_actions")
	seedServer(t, servers, 1, "Sala")
	ctx := context.Background()

	if _, err := repo.Reactivate(ctx, uid(1), eventFor("admin.server_reactivated")); !errors.Is(err, repository.ErrServerNotSuspended) {
		t.Fatalf("err = %v", err)
	}
	if err := repo.RequestDeletion(ctx, uid(1), "otro nombre", uid(99), requestedAt, eventFor("k")); !errors.Is(err, repository.ErrDeleteConfirmationMismatch) {
		t.Fatalf("err = %v", err)
	}
	unserializable := func(int64) (string, any) { return "k", make(chan int) }
	if _, err := repo.Suspend(ctx, uid(1), uid(99), serverSuspendedAt, unserializable); err == nil {
		t.Fatal("tenía que fallar")
	}
	if got := historyOf(t, db); len(got) != 0 {
		t.Errorf("historial = %+v, no debió quedar nada", got)
	}
}
