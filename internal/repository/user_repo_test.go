package repository_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/admin/internal/testutil"
)

func uid(n int) string { return fmt.Sprintf("00000000-0000-0000-0000-%012d", n) }

var base = time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)

type seedUser struct {
	n           int
	name, email string
	// registeredDaysAfter < 0 = sin registro (solo llegó users.profile_updated).
	registeredDaysAfter int
	suspended           bool
}

func seed(t *testing.T, db *sql.DB, users []seedUser) {
	t.Helper()
	for _, u := range users {
		var registered any
		if u.registeredDaysAfter >= 0 {
			registered = base.AddDate(0, 0, u.registeredDaysAfter)
		}
		status, suspendedAt := "active", any(nil)
		if u.suspended {
			status, suspendedAt = "suspended", base.AddDate(0, 1, 0)
		}
		if _, err := db.Exec(`
			INSERT INTO users (user_id, name, email, registered_at, status, suspended_at)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			uid(u.n), u.name, u.email, registered, status, suspendedAt); err != nil {
			t.Fatalf("sembrar %s: %v", u.name, err)
		}
	}
}

func ids(users []model.User) []string {
	out := make([]string, len(users))
	for i, u := range users {
		out[i] = u.ID
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func newUserRepo(t *testing.T) (repository.UserRepository, *sql.DB) {
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "users")
	return repository.NewUserRepository(db), db
}

func TestUserListOrdersNewestFirstAndSkipsUnregistered(t *testing.T) {
	repo, db := newUserRepo(t)
	seed(t, db, []seedUser{
		{1, "Ana", "ana@example.com", 0, false},
		{2, "Beto", "beto@example.com", 2, false},
		{3, "Caro", "caro@example.com", 1, false},
		{4, "Sin registro", "", -1, false}, // llegó el perfil pero no el alta
	})

	users, total, err := repo.List(context.Background(), repository.UserFilter{}, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || !equal(ids(users), []string{uid(2), uid(3), uid(1)}) {
		t.Fatalf("total=%d orden=%v, quería 3 usuarios del más nuevo al más viejo sin el no registrado", total, ids(users))
	}
	if !users[0].CreatedAt.Equal(base.AddDate(0, 0, 2)) || users[0].CreatedAt.Location() != time.UTC {
		t.Errorf("created_at = %v (debe ser la fecha de registro, en UTC)", users[0].CreatedAt)
	}
	if users[0].Email != "beto@example.com" || users[0].Name != "Beto" || users[0].Status != model.UserStatusActive || users[0].SuspendedAt != nil {
		t.Errorf("campos: %+v", users[0])
	}
}

func TestUserListSearchByEmailOrNameIgnoringCase(t *testing.T) {
	repo, db := newUserRepo(t)
	seed(t, db, []seedUser{
		{1, "Manuel Peñalva", "manu@fiuba.ar", 0, false},
		{2, "Ale", "ale@gmail.com", 1, false},
		{3, "Juana", "MANUELA@example.com", 2, false},
	})
	list := func(search string) []string {
		t.Helper()
		users, _, err := repo.List(context.Background(), repository.UserFilter{Search: search}, 20, 0)
		if err != nil {
			t.Fatal(err)
		}
		return ids(users)
	}

	// Coincide por nombre (1) y por email (3), sin importar mayúsculas.
	if got := list("manu"); !equal(got, []string{uid(3), uid(1)}) {
		t.Errorf("manu → %v", got)
	}
	if got := list("GMAIL"); !equal(got, []string{uid(2)}) {
		t.Errorf("GMAIL (email) → %v", got)
	}
	if got := list("peñalva"); !equal(got, []string{uid(1)}) {
		t.Errorf("peñalva (nombre) → %v", got)
	}
	if got := list("nadie"); len(got) != 0 {
		t.Errorf("nadie → %v", got)
	}
	if got := list(""); len(got) != 3 {
		t.Errorf("búsqueda vacía debe listar todo: %v", got)
	}
}

// % y _ son comodines de LIKE: en una búsqueda tienen que tratarse como texto.
func TestUserListSearchEscapesWildcards(t *testing.T) {
	repo, db := newUserRepo(t)
	seed(t, db, []seedUser{
		{1, "Cien%", "cien@example.com", 0, false},
		{2, "Ciento", "cienS@example.com", 1, false},
		{3, "a_b", "ab1@example.com", 2, false},
		{4, "axb", "ab2@example.com", 3, false},
		{5, `Con\barra`, "barra@example.com", 4, false},
	})
	list := func(search string) []string {
		users, _, err := repo.List(context.Background(), repository.UserFilter{Search: search}, 20, 0)
		if err != nil {
			t.Fatal(err)
		}
		return ids(users)
	}

	if got := list("%"); !equal(got, []string{uid(1)}) {
		t.Errorf(`"%%" solo debe coincidir con el nombre que lo tiene: %v`, got)
	}
	if got := list("a_b"); !equal(got, []string{uid(3)}) {
		t.Errorf(`"a_b" no debe actuar como comodín de un carácter: %v`, got)
	}
	if got := list(`\`); !equal(got, []string{uid(5)}) {
		t.Errorf(`"\" debe tratarse como texto: %v`, got)
	}
}

func TestUserListFilterByStatus(t *testing.T) {
	repo, db := newUserRepo(t)
	seed(t, db, []seedUser{
		{1, "Ana", "ana@example.com", 0, false},
		{2, "Beto", "beto@example.com", 1, true},
		{3, "Caro", "caro@example.com", 2, false},
		{4, "Dani", "dani@example.com", 3, true},
	})
	ctx := context.Background()

	active, total, err := repo.List(ctx, repository.UserFilter{Status: model.UserStatusActive}, 20, 0)
	if err != nil || total != 2 || !equal(ids(active), []string{uid(3), uid(1)}) {
		t.Errorf("active: total=%d %v err=%v", total, ids(active), err)
	}
	suspended, total, err := repo.List(ctx, repository.UserFilter{Status: model.UserStatusSuspended}, 20, 0)
	if err != nil || total != 2 || !equal(ids(suspended), []string{uid(4), uid(2)}) {
		t.Errorf("suspended: total=%d %v err=%v", total, ids(suspended), err)
	}
	if suspended[0].SuspendedAt == nil || !suspended[0].SuspendedAt.Equal(base.AddDate(0, 1, 0)) {
		t.Errorf("suspended_at = %v", suspended[0].SuspendedAt)
	}

	// Los filtros se combinan (AND).
	both, total, err := repo.List(ctx, repository.UserFilter{Search: "dani", Status: model.UserStatusActive}, 20, 0)
	if err != nil || total != 0 || len(both) != 0 {
		t.Errorf("dani + active debe ser vacío: total=%d %v err=%v", total, ids(both), err)
	}
	both, total, err = repo.List(ctx, repository.UserFilter{Search: "dani", Status: model.UserStatusSuspended}, 20, 0)
	if err != nil || total != 1 || !equal(ids(both), []string{uid(4)}) {
		t.Errorf("dani + suspended: total=%d %v err=%v", total, ids(both), err)
	}
}

func TestUserListPagination(t *testing.T) {
	repo, db := newUserRepo(t)
	var users []seedUser
	for i := 1; i <= 7; i++ {
		users = append(users, seedUser{i, fmt.Sprintf("U%d", i), fmt.Sprintf("u%d@example.com", i), i, false})
	}
	seed(t, db, users)
	ctx := context.Background()

	// Más nuevos primero: u7, u6, ..., u1.
	page1, total, err := repo.List(ctx, repository.UserFilter{}, 3, 0)
	if err != nil || total != 7 || !equal(ids(page1), []string{uid(7), uid(6), uid(5)}) {
		t.Fatalf("página 1: total=%d %v err=%v", total, ids(page1), err)
	}
	page3, total, err := repo.List(ctx, repository.UserFilter{}, 3, 6)
	if err != nil || total != 7 || !equal(ids(page3), []string{uid(1)}) {
		t.Fatalf("última página parcial: total=%d %v err=%v", total, ids(page3), err)
	}
	// Un offset pasado del final devuelve lista vacía pero conserva el total.
	beyond, total, err := repo.List(ctx, repository.UserFilter{}, 3, 50)
	if err != nil || total != 7 || beyond == nil || len(beyond) != 0 {
		t.Fatalf("offset fuera de rango: total=%d %v err=%v", total, beyond, err)
	}
}

func TestUserListEmptyTable(t *testing.T) {
	repo, _ := newUserRepo(t)
	users, total, err := repo.List(context.Background(), repository.UserFilter{}, 20, 0)
	if err != nil || total != 0 || users == nil || len(users) != 0 {
		t.Fatalf("tabla vacía: total=%d users=%v err=%v", total, users, err)
	}
}

// Desempate estable: dos usuarios registrados en el mismo instante no se
// intercambian entre páginas.
func TestUserListTieBreakIsStable(t *testing.T) {
	repo, db := newUserRepo(t)
	seed(t, db, []seedUser{
		{1, "A", "a@example.com", 0, false},
		{2, "B", "b@example.com", 0, false},
		{3, "C", "c@example.com", 0, false},
	})
	p1, _, _ := repo.List(context.Background(), repository.UserFilter{}, 2, 0)
	p2, _, _ := repo.List(context.Background(), repository.UserFilter{}, 2, 2)
	if !equal(append(ids(p1), ids(p2)...), []string{uid(1), uid(2), uid(3)}) {
		t.Errorf("páginas con empate: %v + %v", ids(p1), ids(p2))
	}
}
