package repository_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/repository"
)

var serverSuspendedAt = time.Date(2026, 11, 6, 9, 30, 0, 0, time.UTC)

type suspensionState struct {
	version     int64
	suspendedAt sql.NullTime
	suspendedBy sql.NullString
}

func suspensionOf(t *testing.T, db *sql.DB, n int) suspensionState {
	t.Helper()
	var s suspensionState
	if err := db.QueryRow(`
		SELECT moderation_version, suspended_at, suspended_by::text
		  FROM servers WHERE server_id = $1`, uid(n)).
		Scan(&s.version, &s.suspendedAt, &s.suspendedBy); err != nil {
		t.Fatal(err)
	}
	return s
}

func suspend(t *testing.T, repo repository.ServerModerationRepository, n int) *model.Server {
	t.Helper()
	s, err := repo.Suspend(context.Background(), uid(n), uid(99), serverSuspendedAt, eventFor("admin.server_suspended"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSuspendServerMarksItAndWritesTheEventInTheSameTransaction(t *testing.T) {
	repo, servers, db := newModerationRepo(t)
	seed(t, db, []seedUser{{n: 9, name: "Manu", email: "manu@example.com", registeredDaysAfter: 0}})
	seedServer(t, servers, 1, "Sala")
	for i, joined := range []bool{true, true, false} {
		if err := servers.SetMembership(context.Background(), repository.Membership{ServerID: uid(1), UserID: uid(100 + i), IsMember: joined, OccurredAt: base}); err != nil {
			t.Fatal(err)
		}
	}

	got := suspend(t, repo, 1)

	if got.ID != uid(1) || got.Name != "Sala" || got.OwnerID != uid(9) || got.MemberCount != 2 {
		t.Errorf("servidor devuelto: %+v", got)
	}
	if got.OwnerName == nil || *got.OwnerName != "Manu" {
		t.Errorf("owner_name = %v, quería Manu", got.OwnerName)
	}
	if got.SuspendedAt == nil || !got.SuspendedAt.Equal(serverSuspendedAt) {
		t.Errorf("suspended_at = %v, quería %v", got.SuspendedAt, serverSuspendedAt)
	}
	st := suspensionOf(t, db, 1)
	if st.version != 1 || !st.suspendedAt.Valid || !st.suspendedAt.Time.Equal(serverSuspendedAt) || st.suspendedBy.String != uid(99) {
		t.Errorf("estado guardado: %+v", st)
	}
	if got, want := outboxRows(t, db), []string{"admin.server_suspended 1"}; !equal(got, want) {
		t.Errorf("outbox = %v, quería %v", got, want)
	}
}

func TestSuspendedServerStaysInTheListingMarked(t *testing.T) {
	repo, servers, _ := newModerationRepo(t)
	seedServer(t, servers, 1, "Sala")
	suspend(t, repo, 1)

	list, total, err := servers.List(context.Background(), repository.ServerFilter{}, 10, 0)
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("total=%d len=%d err=%v", total, len(list), err)
	}
	if list[0].SuspendedAt == nil || !list[0].SuspendedAt.Equal(serverSuspendedAt) {
		t.Errorf("el listado tiene que mostrar suspended_at: %+v", list[0])
	}
}

func TestSuspendingTwiceFailsWithoutAnotherEvent(t *testing.T) {
	repo, servers, db := newModerationRepo(t)
	seedServer(t, servers, 1, "Sala")
	suspend(t, repo, 1)

	_, err := repo.Suspend(context.Background(), uid(1), uid(98), serverSuspendedAt.Add(time.Hour), eventFor("admin.server_suspended"))
	if !errors.Is(err, repository.ErrServerAlreadySuspended) {
		t.Fatalf("err = %v, quería ErrServerAlreadySuspended", err)
	}
	if st := suspensionOf(t, db, 1); st.version != 1 || st.suspendedBy.String != uid(99) || !st.suspendedAt.Time.Equal(serverSuspendedAt) {
		t.Errorf("la segunda suspensión no debió cambiar nada: %+v", st)
	}
	if got := outboxRows(t, db); len(got) != 1 {
		t.Errorf("outbox = %v", got)
	}
}

func TestReactivateLiftsTheSuspensionWithTheNextVersion(t *testing.T) {
	repo, servers, db := newModerationRepo(t)
	seedServer(t, servers, 1, "Sala")
	suspend(t, repo, 1)

	got, err := repo.Reactivate(context.Background(), uid(1), eventFor("admin.server_reactivated"))
	if err != nil {
		t.Fatal(err)
	}

	if got.ID != uid(1) || got.SuspendedAt != nil {
		t.Errorf("servidor devuelto: %+v", got)
	}
	if st := suspensionOf(t, db, 1); st.version != 2 || st.suspendedAt.Valid || st.suspendedBy.Valid {
		t.Errorf("estado guardado: %+v", st)
	}
	if got, want := outboxRows(t, db), []string{"admin.server_suspended 1", "admin.server_reactivated 2"}; !equal(got, want) {
		t.Errorf("outbox = %v, quería %v", got, want)
	}
}

func TestReactivatingAnActiveServerFailsWithoutAnEvent(t *testing.T) {
	repo, servers, db := newModerationRepo(t)
	seedServer(t, servers, 1, "Sala")

	if _, err := repo.Reactivate(context.Background(), uid(1), eventFor("k")); !errors.Is(err, repository.ErrServerNotSuspended) {
		t.Fatalf("err = %v, quería ErrServerNotSuspended", err)
	}
	if st := suspensionOf(t, db, 1); st.version != 0 {
		t.Errorf("no debió cambiar nada: %+v", st)
	}
	if got := outboxRows(t, db); len(got) != 0 {
		t.Errorf("outbox = %v", got)
	}
}

func TestSuspendAndReactivateOfAServerThatDoesNotExistForAdmin(t *testing.T) {
	repo, servers, db := newModerationRepo(t)
	seedServer(t, servers, 2, "Borrado")
	if err := servers.MarkDeleted(context.Background(), uid(2), base); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO servers (server_id) VALUES ($1)`, uid(3)); err != nil { // sin foto
		t.Fatal(err)
	}

	for name, id := range map[string]string{"sin fila": uid(1), "borrado": uid(2), "sin foto": uid(3)} {
		t.Run(name, func(t *testing.T) {
			if _, err := repo.Suspend(context.Background(), id, uid(99), serverSuspendedAt, eventFor("k")); !errors.Is(err, repository.ErrServerNotFound) {
				t.Errorf("Suspend: err = %v, quería ErrServerNotFound", err)
			}
			if _, err := repo.Reactivate(context.Background(), id, eventFor("k")); !errors.Is(err, repository.ErrServerNotFound) {
				t.Errorf("Reactivate: err = %v, quería ErrServerNotFound", err)
			}
		})
	}
	if got := outboxRows(t, db); len(got) != 0 {
		t.Errorf("no debió escribir ningún evento: %v", got)
	}
}

func TestSuspendRollsBackWhenTheEventCannotBeWritten(t *testing.T) {
	repo, servers, db := newModerationRepo(t)
	seedServer(t, servers, 1, "Sala")

	unserializable := func(int64) (string, any) { return "k", make(chan int) }
	if _, err := repo.Suspend(context.Background(), uid(1), uid(99), serverSuspendedAt, unserializable); err == nil {
		t.Fatal("tenía que fallar si el evento no se puede escribir")
	}
	if st := suspensionOf(t, db, 1); st.version != 0 || st.suspendedAt.Valid || st.suspendedBy.Valid {
		t.Errorf("la suspensión no debió quedar sin su evento: %+v", st)
	}
	if got := outboxRows(t, db); len(got) != 0 {
		t.Errorf("outbox = %v", got)
	}
}

func TestReactivateRollsBackWhenTheEventCannotBeWritten(t *testing.T) {
	repo, servers, db := newModerationRepo(t)
	seedServer(t, servers, 1, "Sala")
	suspend(t, repo, 1)

	unserializable := func(int64) (string, any) { return "k", make(chan int) }
	if _, err := repo.Reactivate(context.Background(), uid(1), unserializable); err == nil {
		t.Fatal("tenía que fallar si el evento no se puede escribir")
	}
	if st := suspensionOf(t, db, 1); st.version != 1 || !st.suspendedAt.Valid {
		t.Errorf("tenía que seguir suspendido: %+v", st)
	}
}

// Dos staff suspenden a la vez: una gana y la otra ve el estado nuevo.
func TestConcurrentServerSuspensionsOnlyOneWins(t *testing.T) {
	repo, servers, db := newModerationRepo(t)
	seedServer(t, servers, 1, "Sala")

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := repo.Suspend(context.Background(), uid(1), uid(99), serverSuspendedAt, eventFor("admin.server_suspended"))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	wins, already := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, repository.ErrServerAlreadySuspended):
			already++
		default:
			t.Fatal(err)
		}
	}
	if wins != 1 || already != n-1 {
		t.Errorf("ganó %d y %d recibieron ErrServerAlreadySuspended, quería 1 y %d", wins, already, n-1)
	}
	if got := outboxRows(t, db); len(got) != 1 {
		t.Errorf("se tenía que publicar un solo evento: %v", got)
	}
}

// Un server_updated tardío no puede levantar la suspensión: solo toca nombre,
// dueño y fechas.
func TestServerUpdatedDoesNotLiftTheSuspension(t *testing.T) {
	repo, servers, db := newModerationRepo(t)
	seedServer(t, servers, 1, "Sala")
	suspend(t, repo, 1)

	if err := servers.UpsertServer(context.Background(), snap(1, "Sala renombrada", 8, 0, 60)); err != nil {
		t.Fatal(err)
	}

	if st := suspensionOf(t, db, 1); st.version != 1 || !st.suspendedAt.Valid || st.suspendedBy.String != uid(99) {
		t.Errorf("la suspensión cambió: %+v", st)
	}
}

func TestADeletionCanBeRequestedForASuspendedServer(t *testing.T) {
	repo, servers, db := newModerationRepo(t)
	seedServer(t, servers, 1, "Sala")
	suspend(t, repo, 1)

	if err := repo.RequestDeletion(context.Background(), uid(1), "Sala", uid(99), requestedAt, eventFor("admin.server_deletion_requested")); err != nil {
		t.Fatal(err)
	}
	if got, want := outboxRows(t, db), []string{"admin.server_suspended 1", "admin.server_deletion_requested 2"}; !equal(got, want) {
		t.Errorf("outbox = %v, quería %v", got, want)
	}
}

func TestListSuspendedReturnsTheCurrentVersionOldestFirst(t *testing.T) {
	repo, servers, _ := newModerationRepo(t)
	for n, name := range map[int]string{1: "Uno", 2: "Dos", 3: "Tres", 4: "Cuatro", 5: "Cinco"} {
		seedServer(t, servers, n, name)
	}
	at := func(min int) time.Time { return serverSuspendedAt.Add(time.Duration(min) * time.Minute) }
	ctx := context.Background()
	for _, s := range []struct {
		n   int
		min int
	}{{3, 10}, {1, 20}, {2, 20}} {
		if _, err := repo.Suspend(ctx, uid(s.n), uid(99), at(s.min), eventFor("k")); err != nil {
			t.Fatal(err)
		}
	}
	// El 4 se suspendió y se reactivó; el 5 se suspendió y después se borró.
	if _, err := repo.Suspend(ctx, uid(4), uid(99), at(5), eventFor("k")); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Reactivate(ctx, uid(4), eventFor("k")); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Suspend(ctx, uid(5), uid(99), at(6), eventFor("k")); err != nil {
		t.Fatal(err)
	}
	if err := servers.MarkDeleted(ctx, uid(5), at(7)); err != nil {
		t.Fatal(err)
	}
	// Un segundo ciclo sube la versión del 3.
	if _, err := repo.Reactivate(ctx, uid(3), eventFor("k")); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Suspend(ctx, uid(3), uid(99), at(30), eventFor("k")); err != nil {
		t.Fatal(err)
	}

	got, err := repo.ListSuspended(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, s := range got {
		order = append(order, fmt.Sprintf("%s v%d %s", s.ServerID, s.Version, s.SuspendedAt.Format(time.RFC3339)))
	}
	want := []string{
		fmt.Sprintf("%s v1 %s", uid(1), at(20).Format(time.RFC3339)),
		fmt.Sprintf("%s v1 %s", uid(2), at(20).Format(time.RFC3339)),
		fmt.Sprintf("%s v3 %s", uid(3), at(30).Format(time.RFC3339)),
	}
	if !equal(order, want) {
		t.Errorf("suspendidos = %v, quería %v", order, want)
	}
}

func TestServerListFiltersByStatus(t *testing.T) {
	repo, servers, _ := newModerationRepo(t)
	for n, name := range map[int]string{1: "Uno", 2: "Dos", 3: "Tres"} {
		seedServer(t, servers, n, name)
	}
	suspend(t, repo, 2)
	ctx := context.Background()

	cases := []struct {
		status model.ServerStatus
		want   []string
	}{
		{"", []string{uid(1), uid(2), uid(3)}},
		{model.ServerStatusActive, []string{uid(1), uid(3)}},
		{model.ServerStatusSuspended, []string{uid(2)}},
	}
	for _, tc := range cases {
		t.Run("status="+string(tc.status), func(t *testing.T) {
			got, total, err := servers.List(ctx, repository.ServerFilter{Status: tc.status}, 10, 0)
			if err != nil {
				t.Fatal(err)
			}
			ids := serverIDs(got)
			if len(ids) != len(tc.want) || total != len(tc.want) {
				t.Fatalf("ids = %v total = %d, quería %v", ids, total, tc.want)
			}
			seen := map[string]bool{}
			for _, id := range ids {
				seen[id] = true
			}
			for _, id := range tc.want {
				if !seen[id] {
					t.Errorf("falta %s en %v", id, ids)
				}
			}
		})
	}
}

func TestServerListStatusCombinesWithSearchAndPagination(t *testing.T) {
	repo, servers, _ := newModerationRepo(t)
	for n, name := range map[int]string{1: "Sala A", 2: "Sala B", 3: "Otro"} {
		seedServer(t, servers, n, name)
	}
	suspend(t, repo, 1)
	suspend(t, repo, 3)

	got, total, err := servers.List(context.Background(), repository.ServerFilter{Search: "sala", Status: model.ServerStatusSuspended}, 10, 0)
	if err != nil || total != 1 || len(got) != 1 || got[0].ID != uid(1) {
		t.Errorf("búsqueda + estado: ids=%v total=%d err=%v", serverIDs(got), total, err)
	}
	if got, total, err := servers.List(context.Background(), repository.ServerFilter{Status: model.ServerStatusSuspended}, 1, 1); err != nil || total != 2 || len(got) != 1 {
		t.Errorf("paginación: len=%d total=%d err=%v", len(got), total, err)
	}
}
