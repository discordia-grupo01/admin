package repository_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/admin/internal/testutil"
)

var suspendedAt = time.Date(2026, 11, 2, 15, 0, 0, 0, time.UTC)

func newSuspensionRepo(t *testing.T) (repository.SuspensionRepository, *sql.DB) {
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "users", "staff_members", "outbox_events")
	return repository.NewSuspensionRepository(db), db
}

func eventFor(key string) repository.EventBuilder {
	return func(version int64) (string, any) {
		return key, map[string]any{"version": version}
	}
}

func outboxRows(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT routing_key || ' ' || (payload->>'version') FROM outbox_events ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSuspendChangesStateAndWritesTheEventInTheSameTransaction(t *testing.T) {
	repo, db := newSuspensionRepo(t)
	seed(t, db, []seedUser{{n: 1, name: "Ale", email: "ale@example.com", registeredDaysAfter: 0}})

	u, err := repo.Suspend(context.Background(), uid(1), uid(99), suspendedAt, eventFor("admin.user_suspended"))
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != uid(1) || u.Name != "Ale" || u.Email != "ale@example.com" || u.Status != model.UserStatusSuspended {
		t.Errorf("usuario devuelto: %+v", u)
	}
	if u.SuspendedAt == nil || !u.SuspendedAt.Equal(suspendedAt) || !u.CreatedAt.Equal(base) {
		t.Errorf("fechas: suspended_at=%v created_at=%v", u.SuspendedAt, u.CreatedAt)
	}

	var status string
	var at time.Time
	var by sql.NullString
	var version int64
	if err := db.QueryRow(`SELECT status, suspended_at, suspended_by, suspension_version FROM users WHERE user_id = $1`, uid(1)).
		Scan(&status, &at, &by, &version); err != nil {
		t.Fatal(err)
	}
	if status != "suspended" || !at.Equal(suspendedAt) || by.String != uid(99) || version != 1 {
		t.Errorf("fila: status=%s suspended_at=%v suspended_by=%v version=%d", status, at, by, version)
	}
	if got := outboxRows(t, db); len(got) != 1 || got[0] != "admin.user_suspended 1" {
		t.Errorf("outbox = %v, quería un solo evento con version 1", got)
	}
}

func TestSuspendedUserShowsUpInTheStatusFilter(t *testing.T) {
	repo, db := newSuspensionRepo(t)
	seed(t, db, []seedUser{
		{n: 1, name: "Ale", email: "ale@example.com", registeredDaysAfter: 0},
		{n: 2, name: "Beto", email: "beto@example.com", registeredDaysAfter: 1},
	})
	if _, err := repo.Suspend(context.Background(), uid(1), uid(99), suspendedAt, eventFor("admin.user_suspended")); err != nil {
		t.Fatal(err)
	}
	got, total, err := repository.NewUserRepository(db).List(context.Background(), repository.UserFilter{Status: model.UserStatusSuspended}, 10, 0)
	if err != nil || total != 1 || got[0].ID != uid(1) || got[0].SuspendedAt == nil {
		t.Fatalf("suspendidos: %+v total=%d err=%v", got, total, err)
	}
}

func TestSuspendRefusesWithoutChangingAnything(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		setup func(t *testing.T, db *sql.DB)
		id    string
		want  error
	}{
		{"usuario inexistente", func(*testing.T, *sql.DB) {}, uid(7), repository.ErrUserNotFound},
		{"usuario sin registro todavía", func(t *testing.T, db *sql.DB) {
			seed(t, db, []seedUser{{n: 7, name: "Fantasma", registeredDaysAfter: -1}})
		}, uid(7), repository.ErrUserNotFound},
		{"usuario ya suspendido", func(t *testing.T, db *sql.DB) {
			seed(t, db, []seedUser{{n: 7, name: "Ya", email: "ya@example.com", registeredDaysAfter: 0, suspended: true}})
		}, uid(7), repository.ErrAlreadySuspended},
		{"miembro del staff", func(t *testing.T, db *sql.DB) {
			seed(t, db, []seedUser{{n: 7, name: "Jefe", email: "jefe@example.com", registeredDaysAfter: 0}})
			if _, err := db.Exec(`INSERT INTO staff_members (user_id) VALUES ($1)`, uid(7)); err != nil {
				t.Fatal(err)
			}
		}, uid(7), repository.ErrTargetIsStaff},
		{"staff que no está en la réplica", func(t *testing.T, db *sql.DB) {
			if _, err := db.Exec(`INSERT INTO staff_members (user_id) VALUES ($1)`, uid(7)); err != nil {
				t.Fatal(err)
			}
		}, uid(7), repository.ErrTargetIsStaff},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, db := newSuspensionRepo(t)
			tc.setup(t, db)
			var before string
			_ = db.QueryRow(`SELECT coalesce(string_agg(status || suspension_version::text, ','), '') FROM users`).Scan(&before)

			u, err := repo.Suspend(ctx, tc.id, uid(99), suspendedAt, eventFor("admin.user_suspended"))
			if !errors.Is(err, tc.want) || u != nil {
				t.Fatalf("u=%+v err=%v, quería %v", u, err, tc.want)
			}
			var after string
			_ = db.QueryRow(`SELECT coalesce(string_agg(status || suspension_version::text, ','), '') FROM users`).Scan(&after)
			if after != before {
				t.Errorf("el estado cambió: %q → %q", before, after)
			}
			if got := outboxRows(t, db); len(got) != 0 {
				t.Errorf("no debe escribirse ningún evento: %v", got)
			}
		})
	}
}

// Si el evento no se puede escribir, la suspensión tampoco: nunca queda un
// cambio de estado sin su evento.
func TestSuspendRollsBackWhenTheEventCannotBeStored(t *testing.T) {
	repo, db := newSuspensionRepo(t)
	seed(t, db, []seedUser{{n: 1, name: "Ale", email: "ale@example.com", registeredDaysAfter: 0}})

	_, err := repo.Suspend(context.Background(), uid(1), uid(99), suspendedAt, func(int64) (string, any) {
		return "admin.user_suspended", make(chan int) // no serializable a JSON
	})
	if err == nil {
		t.Fatal("esperaba un error")
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM users WHERE user_id = $1`, uid(1)).Scan(&status); err != nil || status != "active" {
		t.Errorf("status = %q (err %v): la suspensión debe deshacerse", status, err)
	}
}

// Dos staff suspendiendo al mismo usuario a la vez: pasa uno solo, el otro
// recibe "ya suspendido", y hay un único evento.
func TestConcurrentSuspensionsOnlyOneWins(t *testing.T) {
	repo, db := newSuspensionRepo(t)
	seed(t, db, []seedUser{{n: 1, name: "Ale", email: "ale@example.com", registeredDaysAfter: 0}})

	const racers = 8
	errs := make([]error, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = repo.Suspend(context.Background(), uid(1), uid(90+i), suspendedAt, eventFor("admin.user_suspended"))
		}()
	}
	wg.Wait()

	ok, already := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, repository.ErrAlreadySuspended):
			already++
		default:
			t.Errorf("error inesperado: %v", err)
		}
	}
	if ok != 1 || already != racers-1 {
		t.Errorf("ok=%d ya_suspendido=%d, quería 1 y %d", ok, already, racers-1)
	}
	if got := outboxRows(t, db); len(got) != 1 {
		t.Errorf("outbox = %v, quería exactamente un evento", got)
	}
}

func TestReactivateClearsTheSuspensionAndWritesTheEvent(t *testing.T) {
	repo, db := newSuspensionRepo(t)
	seed(t, db, []seedUser{{n: 1, name: "Ale", email: "ale@example.com", registeredDaysAfter: 0}})
	ctx := context.Background()
	if _, err := repo.Suspend(ctx, uid(1), uid(99), suspendedAt, eventFor("admin.user_suspended")); err != nil {
		t.Fatal(err)
	}

	u, err := repo.Reactivate(ctx, uid(1), eventFor("admin.user_reactivated"))
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != uid(1) || u.Name != "Ale" || u.Status != model.UserStatusActive || u.SuspendedAt != nil {
		t.Errorf("usuario devuelto: %+v", u)
	}

	var status string
	var at, by sql.NullString
	var version int64
	if err := db.QueryRow(`SELECT status, suspended_at::text, suspended_by::text, suspension_version FROM users WHERE user_id = $1`, uid(1)).
		Scan(&status, &at, &by, &version); err != nil {
		t.Fatal(err)
	}
	if status != "active" || at.Valid || by.Valid || version != 2 {
		t.Errorf("fila: status=%s suspended_at=%v suspended_by=%v version=%d", status, at, by, version)
	}
	// La versión del evento sube por encima de la de la suspensión.
	if got := outboxRows(t, db); len(got) != 2 || got[0] != "admin.user_suspended 1" || got[1] != "admin.user_reactivated 2" {
		t.Errorf("outbox = %v", got)
	}
}

func TestSuspendReactivateSuspendKeepsVersionsGrowing(t *testing.T) {
	repo, db := newSuspensionRepo(t)
	seed(t, db, []seedUser{{n: 1, name: "Ale", email: "ale@example.com", registeredDaysAfter: 0}})
	ctx := context.Background()

	if _, err := repo.Suspend(ctx, uid(1), uid(99), suspendedAt, eventFor("admin.user_suspended")); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Reactivate(ctx, uid(1), eventFor("admin.user_reactivated")); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Suspend(ctx, uid(1), uid(99), suspendedAt, eventFor("admin.user_suspended")); err != nil {
		t.Fatal(err)
	}
	want := []string{"admin.user_suspended 1", "admin.user_reactivated 2", "admin.user_suspended 3"}
	if got := outboxRows(t, db); !equal(got, want) {
		t.Errorf("outbox = %v, quería %v", got, want)
	}
}

func TestReactivateRefusesWithoutChangingAnything(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, db *sql.DB)
		want  error
	}{
		{"usuario inexistente", func(*testing.T, *sql.DB) {}, repository.ErrUserNotFound},
		{"usuario sin registro todavía", func(t *testing.T, db *sql.DB) {
			seed(t, db, []seedUser{{n: 7, name: "Fantasma", registeredDaysAfter: -1}})
		}, repository.ErrUserNotFound},
		{"usuario activo", func(t *testing.T, db *sql.DB) {
			seed(t, db, []seedUser{{n: 7, name: "Activo", email: "a@example.com", registeredDaysAfter: 0}})
		}, repository.ErrNotSuspended},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, db := newSuspensionRepo(t)
			tc.setup(t, db)
			u, err := repo.Reactivate(context.Background(), uid(7), eventFor("admin.user_reactivated"))
			if !errors.Is(err, tc.want) || u != nil {
				t.Fatalf("u=%+v err=%v, quería %v", u, err, tc.want)
			}
			if got := outboxRows(t, db); len(got) != 0 {
				t.Errorf("no debe escribirse ningún evento: %v", got)
			}
		})
	}
}

func TestReactivateRollsBackWhenTheEventCannotBeStored(t *testing.T) {
	repo, db := newSuspensionRepo(t)
	seed(t, db, []seedUser{{n: 1, name: "Ale", email: "ale@example.com", registeredDaysAfter: 0, suspended: true}})

	_, err := repo.Reactivate(context.Background(), uid(1), func(int64) (string, any) {
		return "admin.user_reactivated", make(chan int) // no serializable a JSON
	})
	if err == nil {
		t.Fatal("esperaba un error")
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM users WHERE user_id = $1`, uid(1)).Scan(&status); err != nil || status != "suspended" {
		t.Errorf("status = %q (err %v): la reactivación debe deshacerse", status, err)
	}
}

func TestConcurrentReactivationsOnlyOneWins(t *testing.T) {
	repo, db := newSuspensionRepo(t)
	seed(t, db, []seedUser{{n: 1, name: "Ale", email: "ale@example.com", registeredDaysAfter: 0, suspended: true}})

	const racers = 8
	errs := make([]error, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = repo.Reactivate(context.Background(), uid(1), eventFor("admin.user_reactivated"))
		}()
	}
	wg.Wait()

	ok, notSuspended := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, repository.ErrNotSuspended):
			notSuspended++
		default:
			t.Errorf("error inesperado: %v", err)
		}
	}
	if ok != 1 || notSuspended != racers-1 {
		t.Errorf("ok=%d no_suspendido=%d, quería 1 y %d", ok, notSuspended, racers-1)
	}
	if got := outboxRows(t, db); len(got) != 1 {
		t.Errorf("outbox = %v, quería exactamente un evento", got)
	}
}

func TestListSuspendedReturnsOnlySuspendedWithCurrentVersion(t *testing.T) {
	repo, db := newSuspensionRepo(t)
	seed(t, db, []seedUser{
		{n: 1, name: "Activo", email: "a@example.com", registeredDaysAfter: 0},
		{n: 2, name: "Suspendido", email: "s@example.com", registeredDaysAfter: 1},
		{n: 3, name: "Suspendido dos veces", email: "d@example.com", registeredDaysAfter: 2},
	})
	ctx := context.Background()
	if got, err := repo.ListSuspended(ctx); err != nil || len(got) != 0 {
		t.Fatalf("sin suspendidos: %v err=%v", got, err)
	}

	if _, err := repo.Suspend(ctx, uid(3), uid(99), suspendedAt, eventFor("admin.user_suspended")); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Reactivate(ctx, uid(3), eventFor("admin.user_reactivated")); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Suspend(ctx, uid(3), uid(99), suspendedAt.Add(time.Hour), eventFor("admin.user_suspended")); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Suspend(ctx, uid(2), uid(99), suspendedAt, eventFor("admin.user_suspended")); err != nil {
		t.Fatal(err)
	}

	got, err := repo.ListSuspended(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("suspendidos: %+v err=%v", got, err)
	}
	if got[0].UserID != uid(2) || got[0].Version != 1 || !got[0].SuspendedAt.Equal(suspendedAt) {
		t.Errorf("primero: %+v", got[0])
	}
	if got[1].UserID != uid(3) || got[1].Version != 3 || !got[1].SuspendedAt.Equal(suspendedAt.Add(time.Hour)) {
		t.Errorf("segundo (suspendido dos veces, versión 3): %+v", got[1])
	}
}
