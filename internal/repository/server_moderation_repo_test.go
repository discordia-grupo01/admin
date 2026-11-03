package repository_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/admin/internal/testutil"
)

var requestedAt = time.Date(2026, 11, 5, 15, 0, 0, 0, time.UTC)

func newModerationRepo(t *testing.T) (repository.ServerModerationRepository, repository.ServerRepository, *sql.DB) {
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "servers", "server_members", "users", "outbox_events")
	return repository.NewServerModerationRepository(db), repository.NewServerRepository(db), db
}

// moderationState es lo que admin guarda de la moderación de un servidor.
type moderationState struct {
	version     int64
	requestedAt sql.NullTime
	requestedBy sql.NullString
	deletedAt   sql.NullTime
}

func stateOf(t *testing.T, db *sql.DB, n int) moderationState {
	t.Helper()
	var s moderationState
	if err := db.QueryRow(`
		SELECT moderation_version, deletion_requested_at, deletion_requested_by::text, deleted_at
		  FROM servers WHERE server_id = $1`, uid(n)).
		Scan(&s.version, &s.requestedAt, &s.requestedBy, &s.deletedAt); err != nil {
		t.Fatal(err)
	}
	return s
}

func seedServer(t *testing.T, servers repository.ServerRepository, n int, name string) {
	t.Helper()
	if err := servers.UpsertServer(context.Background(), snap(n, name, 9, 0, 1)); err != nil {
		t.Fatal(err)
	}
}

func TestRequestDeletionRecordsTheRequestAndWritesTheEventInTheSameTransaction(t *testing.T) {
	repo, servers, db := newModerationRepo(t)
	seedServer(t, servers, 1, "Sala")

	err := repo.RequestDeletion(context.Background(), uid(1), "Sala", uid(99), requestedAt, eventFor("admin.server_deletion_requested"))
	if err != nil {
		t.Fatal(err)
	}

	st := stateOf(t, db, 1)
	if st.version != 1 || !st.requestedAt.Valid || !st.requestedAt.Time.Equal(requestedAt) || st.requestedBy.String != uid(99) {
		t.Errorf("estado guardado: %+v", st)
	}
	if st.deletedAt.Valid {
		t.Error("admin no debe marcar el servidor como borrado: eso lo hace servers.server_deleted")
	}
	if got, want := outboxRows(t, db), []string{"admin.server_deletion_requested 1"}; !equal(got, want) {
		t.Errorf("outbox = %v, quería %v", got, want)
	}

	// Hasta que servers confirme, el servidor sigue en el listado.
	list, total, err := servers.List(context.Background(), repository.ServerFilter{}, 10, 0)
	if err != nil || total != 1 || len(list) != 1 {
		t.Errorf("el servidor tiene que seguir en el listado: total=%d len=%d err=%v", total, len(list), err)
	}
}

func TestRequestDeletionIgnoresSpacesAroundTheName(t *testing.T) {
	repo, servers, db := newModerationRepo(t)
	seedServer(t, servers, 1, "Sala")

	if err := repo.RequestDeletion(context.Background(), uid(1), "  Sala \t", uid(99), requestedAt, eventFor("k")); err != nil {
		t.Fatalf("los espacios en los bordes no deberían importar: %v", err)
	}
	if got := outboxRows(t, db); len(got) != 1 {
		t.Errorf("outbox = %v", got)
	}
}

func TestRequestDeletionWithAWrongNameChangesNothing(t *testing.T) {
	for _, name := range []string{"", "   ", "sala", "Otra", "Sala Grande"} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			repo, servers, db := newModerationRepo(t)
			seedServer(t, servers, 1, "Sala")

			err := repo.RequestDeletion(context.Background(), uid(1), name, uid(99), requestedAt, eventFor("k"))
			if !errors.Is(err, repository.ErrDeleteConfirmationMismatch) {
				t.Fatalf("err = %v, quería ErrDeleteConfirmationMismatch", err)
			}
			if st := stateOf(t, db, 1); st.version != 0 || st.requestedAt.Valid || st.requestedBy.Valid {
				t.Errorf("no debió cambiar nada: %+v", st)
			}
			if got := outboxRows(t, db); len(got) != 0 {
				t.Errorf("no debió escribir ningún evento: %v", got)
			}
		})
	}
}

func TestRequestDeletionOfAServerThatDoesNotExistForAdmin(t *testing.T) {
	repo, servers, db := newModerationRepo(t)
	seedServer(t, servers, 2, "Borrado")
	if err := servers.MarkDeleted(context.Background(), uid(2), base); err != nil {
		t.Fatal(err)
	}
	// Una fila sin foto: llegó un member_joined o un server_deleted antes que el alta.
	if _, err := db.Exec(`INSERT INTO servers (server_id) VALUES ($1)`, uid(3)); err != nil {
		t.Fatal(err)
	}

	for name, id := range map[string]string{
		"sin fila": uid(1),
		"borrado":  uid(2),
		"sin foto": uid(3),
	} {
		t.Run(name, func(t *testing.T) {
			err := repo.RequestDeletion(context.Background(), id, "Borrado", uid(99), requestedAt, eventFor("k"))
			if !errors.Is(err, repository.ErrServerNotFound) {
				t.Fatalf("err = %v, quería ErrServerNotFound", err)
			}
		})
	}
	if got := outboxRows(t, db); len(got) != 0 {
		t.Errorf("no debió escribir ningún evento: %v", got)
	}
}

func TestRequestDeletionCanBeRepeatedWithANewVersionEachTime(t *testing.T) {
	repo, servers, db := newModerationRepo(t)
	seedServer(t, servers, 1, "Sala")

	for i := 0; i < 2; i++ {
		if err := repo.RequestDeletion(context.Background(), uid(1), "Sala", uid(90+i), requestedAt.Add(time.Duration(i)*time.Minute), eventFor("k")); err != nil {
			t.Fatal(err)
		}
	}

	if st := stateOf(t, db, 1); st.version != 2 || st.requestedBy.String != uid(91) || !st.requestedAt.Time.Equal(requestedAt.Add(time.Minute)) {
		t.Errorf("tiene que quedar el último pedido: %+v", st)
	}
	if got, want := outboxRows(t, db), []string{"k 1", "k 2"}; !equal(got, want) {
		t.Errorf("outbox = %v, quería %v", got, want)
	}
}

func TestRequestDeletionRollsBackWhenTheEventCannotBeWritten(t *testing.T) {
	repo, servers, db := newModerationRepo(t)
	seedServer(t, servers, 1, "Sala")

	unserializable := func(int64) (string, any) { return "k", make(chan int) }
	if err := repo.RequestDeletion(context.Background(), uid(1), "Sala", uid(99), requestedAt, unserializable); err == nil {
		t.Fatal("tenía que fallar si el evento no se puede escribir")
	}

	if st := stateOf(t, db, 1); st.version != 0 || st.requestedAt.Valid {
		t.Errorf("el pedido no debió quedar registrado sin su evento: %+v", st)
	}
	if got := outboxRows(t, db); len(got) != 0 {
		t.Errorf("outbox = %v", got)
	}
}

// Pedidos simultáneos sobre el mismo servidor: el bloqueo de la fila hace que
// cada uno reciba una versión distinta, sin huecos ni repetidas.
func TestConcurrentDeletionRequestsGetDistinctVersions(t *testing.T) {
	repo, servers, db := newModerationRepo(t)
	seedServer(t, servers, 1, "Sala")

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- repo.RequestDeletion(context.Background(), uid(1), "Sala", uid(99), requestedAt, eventFor("k"))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	got := outboxRows(t, db)
	sort.Strings(got)
	want := make([]string, n)
	for i := range want {
		want[i] = fmt.Sprintf("k %d", i+1)
	}
	sort.Strings(want)
	if !equal(got, want) {
		t.Errorf("versiones publicadas = %v, quería %v", got, want)
	}
	if st := stateOf(t, db, 1); st.version != n {
		t.Errorf("moderation_version = %d, quería %d", st.version, n)
	}
}

func TestDeletionConfirmedByServersHidesTheServerAndKeepsTheRequest(t *testing.T) {
	repo, servers, db := newModerationRepo(t)
	seedServer(t, servers, 1, "Sala")
	if err := repo.RequestDeletion(context.Background(), uid(1), "Sala", uid(99), requestedAt, eventFor("k")); err != nil {
		t.Fatal(err)
	}

	if err := servers.MarkDeleted(context.Background(), uid(1), requestedAt.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}

	list, total, err := servers.List(context.Background(), repository.ServerFilter{}, 10, 0)
	if err != nil || total != 0 || len(list) != 0 {
		t.Errorf("tras servers.server_deleted el servidor sale del listado: total=%d err=%v", total, err)
	}
	st := stateOf(t, db, 1)
	if !st.deletedAt.Valid || !st.requestedAt.Valid || st.requestedBy.String != uid(99) {
		t.Errorf("tiene que quedar el borrado y también el pedido: %+v", st)
	}
	if err := repo.RequestDeletion(context.Background(), uid(1), "Sala", uid(99), requestedAt, eventFor("k")); !errors.Is(err, repository.ErrServerNotFound) {
		t.Errorf("un servidor ya borrado no se puede volver a eliminar: %v", err)
	}
}

// Un server_updated tardío solo toca nombre, dueño y fechas: no puede pisar la
// versión de moderación ni el pedido registrado.
func TestServerUpdatedDoesNotTouchTheModerationState(t *testing.T) {
	repo, servers, db := newModerationRepo(t)
	seedServer(t, servers, 1, "Sala")
	if err := repo.RequestDeletion(context.Background(), uid(1), "Sala", uid(99), requestedAt, eventFor("k")); err != nil {
		t.Fatal(err)
	}

	if err := servers.UpsertServer(context.Background(), snap(1, "Sala renombrada", 8, 0, 60)); err != nil {
		t.Fatal(err)
	}

	st := stateOf(t, db, 1)
	if st.version != 1 || !st.requestedAt.Valid || st.requestedBy.String != uid(99) {
		t.Errorf("el estado de moderación cambió: %+v", st)
	}
}
