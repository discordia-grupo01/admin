package repository_test

import (
	"context"
	"database/sql"
	"errors"
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

// seed siembra usuarios con el email verificado; markPending deja sin
// verificar a los que se le pasen.

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
			INSERT INTO users (user_id, name, email, registered_at, status, suspended_at, email_verified)
			VALUES ($1, $2, $3, $4, $5, $6, TRUE)`,
			uid(u.n), u.name, u.email, registered, status, suspendedAt); err != nil {
			t.Fatalf("sembrar %s: %v", u.name, err)
		}
	}
}

func markPending(t *testing.T, db *sql.DB, ns ...int) {
	t.Helper()
	for _, n := range ns {
		if _, err := db.Exec(`UPDATE users SET email_verified = FALSE WHERE user_id = $1`, uid(n)); err != nil {
			t.Fatalf("dejar pendiente a %d: %v", n, err)
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

// Un usuario que no confirmó su email está pendiente, salvo que esté
// suspendido: la suspensión manda.
func TestUserListPendingStatus(t *testing.T) {
	repo, db := newUserRepo(t)
	seed(t, db, []seedUser{
		{1, "Ana", "ana@example.com", 0, false},
		{2, "Beto", "beto@example.com", 1, false},
		{3, "Caro", "caro@example.com", 2, true},
		{4, "Dani", "dani@example.com", 3, false},
	})
	markPending(t, db, 2, 3)
	ctx := context.Background()

	all := listAll(t, repo)
	got := map[string]model.UserStatus{}
	for _, u := range all {
		got[u.ID] = u.Status
	}
	want := map[string]model.UserStatus{
		uid(1): model.UserStatusActive,
		uid(2): model.UserStatusPending,
		uid(3): model.UserStatusSuspended,
		uid(4): model.UserStatusActive,
	}
	for id, s := range want {
		if got[id] != s {
			t.Errorf("%s: estado %q, quería %q", id, got[id], s)
		}
	}

	pending, total, err := repo.List(ctx, repository.UserFilter{Status: model.UserStatusPending}, 20, 0)
	if err != nil || total != 1 || !equal(ids(pending), []string{uid(2)}) {
		t.Errorf("pending: total=%d %v err=%v", total, ids(pending), err)
	}
	active, total, err := repo.List(ctx, repository.UserFilter{Status: model.UserStatusActive}, 20, 0)
	if err != nil || total != 2 || !equal(ids(active), []string{uid(4), uid(1)}) {
		t.Errorf("active no debe incluir pendientes: total=%d %v err=%v", total, ids(active), err)
	}
	suspended, total, err := repo.List(ctx, repository.UserFilter{Status: model.UserStatusSuspended}, 20, 0)
	if err != nil || total != 1 || !equal(ids(suspended), []string{uid(3)}) {
		t.Errorf("suspended incluye al suspendido sin verificar: total=%d %v err=%v", total, ids(suspended), err)
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

func listAll(t *testing.T, repo repository.UserRepository) []model.User {
	t.Helper()
	users, _, err := repo.List(context.Background(), repository.UserFilter{}, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	return users
}

func TestUpsertRegistrationAddsTheUserToTheListing(t *testing.T) {
	repo, _ := newUserRepo(t)
	ctx := context.Background()
	reg := repository.Registration{UserID: uid(1), Email: "manu@example.com", Name: "Manu", RegisteredAt: base, EmailVerified: true}

	if err := repo.UpsertRegistration(ctx, reg); err != nil {
		t.Fatal(err)
	}
	// Repetirlo (at-least-once) deja exactamente lo mismo.
	if err := repo.UpsertRegistration(ctx, reg); err != nil {
		t.Fatal(err)
	}

	users := listAll(t, repo)
	if len(users) != 1 || users[0].ID != uid(1) || users[0].Email != "manu@example.com" || users[0].Name != "Manu" ||
		users[0].Status != model.UserStatusActive || !users[0].CreatedAt.Equal(base) {
		t.Fatalf("listado: %+v", users)
	}
}

// Un alta con email sin verificar aparece como pendiente hasta que llega
// users.email_verified.
func TestRegistrationPendingUntilEmailVerified(t *testing.T) {
	repo, _ := newUserRepo(t)
	ctx := context.Background()
	if err := repo.UpsertRegistration(ctx, repository.Registration{UserID: uid(1), Email: "a@example.com", Name: "A", RegisteredAt: base}); err != nil {
		t.Fatal(err)
	}
	if users := listAll(t, repo); len(users) != 1 || users[0].Status != model.UserStatusPending {
		t.Fatalf("sin verificar debe estar pendiente: %+v", users)
	}

	// Repetido (at-least-once) deja lo mismo.
	for i := 0; i < 2; i++ {
		if err := repo.MarkEmailVerified(ctx, uid(1)); err != nil {
			t.Fatal(err)
		}
	}
	if users := listAll(t, repo); len(users) != 1 || users[0].Status != model.UserStatusActive {
		t.Fatalf("verificado debe estar activo: %+v", users)
	}
}

// La verificación puede llegar antes que el alta, y un alta posterior (o un
// backfill) con email_verified = false no la deshace.
func TestEmailVerifiedBeforeRegistration(t *testing.T) {
	repo, _ := newUserRepo(t)
	ctx := context.Background()

	if err := repo.MarkEmailVerified(ctx, uid(1)); err != nil {
		t.Fatal(err)
	}
	if users := listAll(t, repo); len(users) != 0 {
		t.Fatalf("sin registro no debe aparecer: %+v", users)
	}

	if err := repo.UpsertRegistration(ctx, repository.Registration{UserID: uid(1), Email: "a@example.com", Name: "A", RegisteredAt: base}); err != nil {
		t.Fatal(err)
	}
	if users := listAll(t, repo); len(users) != 1 || users[0].Status != model.UserStatusActive {
		t.Fatalf("debe aparecer activo, la verificación ya había llegado: %+v", users)
	}
}

// Verificar el email no levanta una suspensión.
func TestEmailVerifiedDoesNotTouchSuspension(t *testing.T) {
	repo, db := newUserRepo(t)
	seed(t, db, []seedUser{{1, "Ana", "ana@example.com", 0, true}})
	markPending(t, db, 1)

	if err := repo.MarkEmailVerified(context.Background(), uid(1)); err != nil {
		t.Fatal(err)
	}
	users := listAll(t, repo)
	if len(users) != 1 || users[0].Status != model.UserStatusSuspended || users[0].SuspendedAt == nil {
		t.Fatalf("la suspensión debe seguir intacta: %+v", users)
	}
}

func TestUpsertProfileKeepsOnlyNewerVersions(t *testing.T) {
	repo, _ := newUserRepo(t)
	ctx := context.Background()
	if err := repo.UpsertRegistration(ctx, repository.Registration{UserID: uid(1), Email: "a@example.com", Name: "Registro", RegisteredAt: base}); err != nil {
		t.Fatal(err)
	}
	name := func() string { return listAll(t, repo)[0].Name }

	steps := []struct {
		profile repository.Profile
		want    string
	}{
		{repository.Profile{UserID: uid(1), Name: "v1", Version: 1}, "v1"},          // pisa al nombre del registro
		{repository.Profile{UserID: uid(1), Name: "v3", Version: 3}, "v3"},          // más nueva
		{repository.Profile{UserID: uid(1), Name: "v2 tardío", Version: 2}, "v3"},   // fuera de orden: se ignora
		{repository.Profile{UserID: uid(1), Name: "v3 repetido", Version: 3}, "v3"}, // repetido: se ignora
	}
	for _, s := range steps {
		if err := repo.UpsertProfile(ctx, s.profile); err != nil {
			t.Fatal(err)
		}
		if got := name(); got != s.want {
			t.Fatalf("tras la versión %d el nombre es %q, quería %q", s.profile.Version, got, s.want)
		}
	}
}

// El perfil puede llegar antes que el alta: el usuario no aparece hasta que
// llega el registro, y entonces conserva el nombre del perfil (más nuevo).
func TestProfileBeforeRegistration(t *testing.T) {
	repo, _ := newUserRepo(t)
	ctx := context.Background()

	if err := repo.UpsertProfile(ctx, repository.Profile{UserID: uid(1), Name: "Del perfil", Version: 2}); err != nil {
		t.Fatal(err)
	}
	if users := listAll(t, repo); len(users) != 0 {
		t.Fatalf("sin registro no debe aparecer: %+v", users)
	}

	if err := repo.UpsertRegistration(ctx, repository.Registration{UserID: uid(1), Email: "a@example.com", Name: "Del registro", RegisteredAt: base}); err != nil {
		t.Fatal(err)
	}
	users := listAll(t, repo)
	if len(users) != 1 || users[0].Name != "Del perfil" || users[0].Email != "a@example.com" {
		t.Fatalf("debe aparecer con el nombre del perfil y el email del registro: %+v", users)
	}
}

// Reaplicar el alta (por ejemplo, un backfill) no puede levantar una suspensión
// ni borrar el estado: el estado de suspensión es de admin.
func TestRegistrationDoesNotTouchSuspension(t *testing.T) {
	repo, db := newUserRepo(t)
	seed(t, db, []seedUser{{1, "Ana", "ana@example.com", 0, true}})

	if err := repo.UpsertRegistration(context.Background(), repository.Registration{UserID: uid(1), Email: "ana@example.com", Name: "Ana", RegisteredAt: base}); err != nil {
		t.Fatal(err)
	}
	users := listAll(t, repo)
	if len(users) != 1 || users[0].Status != model.UserStatusSuspended || users[0].SuspendedAt == nil {
		t.Fatalf("la suspensión debe seguir intacta: %+v", users)
	}
}

func TestUpsertRejectsInvalidUserID(t *testing.T) {
	repo, _ := newUserRepo(t)
	if err := repo.UpsertRegistration(context.Background(), repository.Registration{UserID: "no-es-uuid", Email: "a@b.c", RegisteredAt: base}); err == nil {
		t.Error("UpsertRegistration debería fallar con un id inválido")
	}
	if err := repo.UpsertProfile(context.Background(), repository.Profile{UserID: "no-es-uuid", Name: "n", Version: 1}); err == nil {
		t.Error("UpsertProfile debería fallar con un id inválido")
	}
	if err := repo.MarkEmailVerified(context.Background(), "no-es-uuid"); err == nil {
		t.Error("MarkEmailVerified debería fallar con un id inválido")
	}
}

func TestFindIDByEmail(t *testing.T) {
	repo, db := newUserRepo(t)
	seed(t, db, []seedUser{
		{1, "Manu", "Manu@Example.com", 0, false},
		{2, "Sin registro", "fantasma@example.com", -1, false}, // llegó el perfil, no el alta
	})
	ctx := context.Background()

	// Coincidencia exacta, sin distinguir mayúsculas.
	for _, email := range []string{"manu@example.com", "MANU@EXAMPLE.COM", "Manu@Example.com"} {
		if id, err := repo.FindIDByEmail(ctx, email); err != nil || id != uid(1) {
			t.Errorf("%q → id=%q err=%v", email, id, err)
		}
	}
	// No es una búsqueda parcial: un fragmento no alcanza.
	for _, email := range []string{"manu", "manu@example", "otro@example.com", "", "%"} {
		if _, err := repo.FindIDByEmail(ctx, email); !errors.Is(err, repository.ErrUserNotFound) {
			t.Errorf("%q debería ser ErrUserNotFound, obtuve %v", email, err)
		}
	}
	// Un usuario sin alta registrada todavía no se puede encontrar.
	if _, err := repo.FindIDByEmail(ctx, "fantasma@example.com"); !errors.Is(err, repository.ErrUserNotFound) {
		t.Errorf("sin registro: err = %v", err)
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

func TestUpsertProfileStoresTheAvatar(t *testing.T) {
	repo, _ := newUserRepo(t)
	ctx := context.Background()
	if err := repo.UpsertRegistration(ctx, repository.Registration{UserID: uid(1), Email: "a@example.com", Name: "Ana", RegisteredAt: base}); err != nil {
		t.Fatal(err)
	}
	avatar := func() *string { return listAll(t, repo)[0].AvatarURL }
	if got := avatar(); got != nil {
		t.Fatalf("sin perfil el avatar debe ser null, es %q", *got)
	}

	steps := []struct {
		profile repository.Profile
		want    string
	}{
		{repository.Profile{UserID: uid(1), Name: "Ana", AvatarURL: "/uploads/profile-images/a.png", Version: 1}, "/uploads/profile-images/a.png"},
		{repository.Profile{UserID: uid(1), Name: "Ana", AvatarURL: "/uploads/profile-images/b.png", Version: 1}, "/uploads/profile-images/a.png"}, // repetido: se ignora
		{repository.Profile{UserID: uid(1), Name: "Ana", AvatarURL: "", Version: 2}, ""},                                                           // sacó la foto
	}
	for _, s := range steps {
		if err := repo.UpsertProfile(ctx, s.profile); err != nil {
			t.Fatal(err)
		}
		got := avatar()
		if s.want == "" && got != nil || s.want != "" && (got == nil || *got != s.want) {
			t.Fatalf("tras la versión %d el avatar es %v, quería %q", s.profile.Version, got, s.want)
		}
	}
}

// Los usuarios que admin ya tenía antes de guardar el avatar quedan con la
// columna en NULL: el backfill de identify vuelve a mandar la misma versión y
// tiene que completarla.
func TestUpsertProfileWithTheSameVersionFillsAnUnknownAvatar(t *testing.T) {
	repo, db := newUserRepo(t)
	ctx := context.Background()
	if err := repo.UpsertRegistration(ctx, repository.Registration{UserID: uid(1), Email: "a@example.com", Name: "Ana", RegisteredAt: base}); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertProfile(ctx, repository.Profile{UserID: uid(1), Name: "Ana", Version: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE users SET avatar_url = NULL WHERE user_id = $1`, uid(1)); err != nil {
		t.Fatal(err)
	}

	if err := repo.UpsertProfile(ctx, repository.Profile{UserID: uid(1), Name: "Ana", AvatarURL: "https://cdn.example.com/a.png", Version: 3}); err != nil {
		t.Fatal(err)
	}
	if got := listAll(t, repo)[0].AvatarURL; got == nil || *got != "https://cdn.example.com/a.png" {
		t.Fatalf("avatar = %v, quería el del backfill", got)
	}
}
