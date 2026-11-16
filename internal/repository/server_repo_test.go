package repository_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/admin/internal/testutil"
)

func newServerRepo(t *testing.T) (repository.ServerRepository, *sql.DB) {
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "servers", "server_members", "users")
	return repository.NewServerRepository(db), db
}

func snap(n int, name string, ownerN int, createdDaysAfter int, occurredMin int) repository.ServerSnapshot {
	return repository.ServerSnapshot{
		ServerID:   uid(n),
		Name:       name,
		OwnerID:    uid(ownerN),
		CreatedAt:  base.AddDate(0, 0, createdDaysAfter),
		OccurredAt: base.Add(time.Duration(occurredMin) * time.Minute),
	}
}

func serverIDs(servers []model.Server) []string {
	out := make([]string, len(servers))
	for i, s := range servers {
		out[i] = s.ID
	}
	return out
}

func TestServerListOrdersByCreationDescAndCountsMembers(t *testing.T) {
	repo, _ := newServerRepo(t)
	ctx := context.Background()
	for _, s := range []repository.ServerSnapshot{snap(1, "Viejo", 9, 0, 1), snap(2, "Nuevo", 9, 5, 1), snap(3, "Medio", 9, 2, 1)} {
		if err := repo.UpsertServer(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	for i, joined := range []bool{true, true, false} { // dos miembros, uno que se fue
		if err := repo.SetMembership(ctx, repository.Membership{ServerID: uid(2), UserID: uid(100 + i), IsMember: joined, OccurredAt: base}); err != nil {
			t.Fatal(err)
		}
	}

	got, total, err := repo.List(ctx, repository.ServerFilter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{uid(2), uid(3), uid(1)}; !equal(serverIDs(got), want) || total != 3 {
		t.Fatalf("orden = %v total=%d, quería %v", serverIDs(got), total, want)
	}
	if got[0].MemberCount != 2 || got[1].MemberCount != 0 {
		t.Errorf("member_count = %d y %d, quería 2 y 0", got[0].MemberCount, got[1].MemberCount)
	}
	if got[0].Name != "Nuevo" || got[0].OwnerID != uid(9) || !got[0].CreatedAt.Equal(base.AddDate(0, 0, 5)) {
		t.Errorf("datos del servidor: %+v", got[0])
	}
}

func TestServerListOwnerNameIsNullUntilOwnerIsReplicated(t *testing.T) {
	repo, db := newServerRepo(t)
	ctx := context.Background()
	if err := repo.UpsertServer(ctx, snap(1, "Sala", 7, 0, 1)); err != nil {
		t.Fatal(err)
	}
	got, _, err := repo.List(ctx, repository.ServerFilter{}, 10, 0)
	if err != nil || len(got) != 1 || got[0].OwnerName != nil {
		t.Fatalf("sin el dueño en la réplica owner_name debe ser null: %+v err=%v", got, err)
	}

	seed(t, db, []seedUser{{n: 7, name: "Manu", email: "manu@example.com", registeredDaysAfter: 0}})
	got, _, err = repo.List(ctx, repository.ServerFilter{}, 10, 0)
	if err != nil || got[0].OwnerName == nil || *got[0].OwnerName != "Manu" {
		t.Fatalf("owner_name = %v err=%v", got[0].OwnerName, err)
	}
}

func TestServerListSearchesByNameCaseInsensitiveAndLiteralWildcards(t *testing.T) {
	repo, _ := newServerRepo(t)
	ctx := context.Background()
	for i, name := range []string{"Gamers Argentinos", "Club de Ajedrez", "100% Gamers", "a_b"} {
		if err := repo.UpsertServer(ctx, snap(i+1, name, 9, i, 1)); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string][]string{
		"gamers": {uid(3), uid(1)},
		"AJED":   {uid(2)},
		"%":      {uid(3)}, // % es texto, no comodín
		"_":      {uid(4)},
		"nada":   {},
	}
	for search, want := range cases {
		got, total, err := repo.List(ctx, repository.ServerFilter{Search: search}, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !equal(serverIDs(got), want) || total != len(want) {
			t.Errorf("search %q: %v total=%d, quería %v", search, serverIDs(got), total, want)
		}
	}
}

func TestServerListPaginates(t *testing.T) {
	repo, _ := newServerRepo(t)
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		if err := repo.UpsertServer(ctx, snap(i, "S", 9, i, 1)); err != nil { // el 5 es el más nuevo
			t.Fatal(err)
		}
	}
	page, total, err := repo.List(ctx, repository.ServerFilter{}, 2, 2)
	if err != nil || total != 5 || !equal(serverIDs(page), []string{uid(3), uid(2)}) {
		t.Fatalf("página 2: %v total=%d err=%v", serverIDs(page), total, err)
	}
	past, total, err := repo.List(ctx, repository.ServerFilter{}, 2, 50)
	if err != nil || total != 5 || len(past) != 0 {
		t.Fatalf("offset pasado: %v total=%d err=%v", past, total, err)
	}
}

func TestUpsertServerKeepsNewestSnapshot(t *testing.T) {
	repo, _ := newServerRepo(t)
	ctx := context.Background()
	steps := []struct {
		s    repository.ServerSnapshot
		want string
	}{
		{snap(1, "Segundo", 9, 0, 10), "Segundo"},
		{snap(1, "Primero", 9, 0, 5), "Segundo"},  // llegó tarde: no pisa
		{snap(1, "Segundo", 9, 0, 10), "Segundo"}, // repetido
		{snap(1, "Tercero", 8, 0, 20), "Tercero"},
	}
	for i, st := range steps {
		if err := repo.UpsertServer(ctx, st.s); err != nil {
			t.Fatal(err)
		}
		got, _, err := repo.List(ctx, repository.ServerFilter{}, 10, 0)
		if err != nil || len(got) != 1 || got[0].Name != st.want {
			t.Fatalf("paso %d: %+v err=%v, quería nombre %q", i, got, err, st.want)
		}
	}
	got, _, _ := repo.List(ctx, repository.ServerFilter{}, 10, 0)
	if got[0].OwnerID != uid(8) {
		t.Errorf("owner_id = %s, quería el del último snapshot", got[0].OwnerID)
	}
}

func TestSetMembershipIgnoresOlderEvents(t *testing.T) {
	repo, _ := newServerRepo(t)
	ctx := context.Background()
	if err := repo.UpsertServer(ctx, snap(1, "S", 9, 0, 1)); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		got, _, err := repo.List(ctx, repository.ServerFilter{}, 10, 0)
		if err != nil || len(got) != 1 {
			t.Fatalf("List: %+v err=%v", got, err)
		}
		return got[0].MemberCount
	}
	m := func(joined bool, min int) repository.Membership {
		return repository.Membership{ServerID: uid(1), UserID: uid(50), IsMember: joined, OccurredAt: base.Add(time.Duration(min) * time.Minute)}
	}

	// member_left (min 10) llega antes que el member_joined (min 5) que lo precede.
	for _, step := range []struct {
		m    repository.Membership
		want int
	}{{m(false, 10), 0}, {m(true, 5), 0}, {m(false, 10), 0}, {m(true, 20), 1}, {m(true, 20), 1}} {
		if err := repo.SetMembership(ctx, step.m); err != nil {
			t.Fatal(err)
		}
		if got := count(); got != step.want {
			t.Fatalf("tras %+v member_count = %d, quería %d", step.m, got, step.want)
		}
	}
}

func TestMarkDeletedHidesServerAndSurvivesLateEvents(t *testing.T) {
	repo, db := newServerRepo(t)
	ctx := context.Background()
	if err := repo.UpsertServer(ctx, snap(1, "Borrame", 9, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertServer(ctx, snap(2, "Queda", 9, 1, 1)); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetMembership(ctx, repository.Membership{ServerID: uid(1), UserID: uid(50), IsMember: true, OccurredAt: base}); err != nil {
		t.Fatal(err)
	}

	for range 2 { // repetirlo es inofensivo
		if err := repo.MarkDeleted(ctx, uid(1), base.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	// Un server_updated atrasado (o más nuevo por reloj) no resucita el servidor.
	if err := repo.UpsertServer(ctx, snap(1, "Borrame", 9, 0, 500)); err != nil {
		t.Fatal(err)
	}

	got, total, err := repo.List(ctx, repository.ServerFilter{}, 10, 0)
	if err != nil || total != 1 || !equal(serverIDs(got), []string{uid(2)}) {
		t.Fatalf("tras borrar: %v total=%d err=%v", serverIDs(got), total, err)
	}
	var members int
	if err := db.QueryRow(`SELECT count(*) FROM server_members WHERE server_id = $1`, uid(1)).Scan(&members); err != nil || members != 0 {
		t.Errorf("membresías del servidor borrado = %d (err %v), quería 0", members, err)
	}
}

func TestMarkDeletedBeforeSnapshotKeepsServerHidden(t *testing.T) {
	repo, _ := newServerRepo(t)
	ctx := context.Background()
	if err := repo.MarkDeleted(ctx, uid(1), base); err != nil { // el delete llegó primero
		t.Fatal(err)
	}
	if err := repo.UpsertServer(ctx, snap(1, "Fantasma", 9, 0, 1)); err != nil {
		t.Fatal(err)
	}
	got, total, err := repo.List(ctx, repository.ServerFilter{}, 10, 0)
	if err != nil || total != 0 || len(got) != 0 {
		t.Fatalf("un servidor borrado no debe aparecer: %v total=%d err=%v", got, total, err)
	}
}

func TestServerListHidesServersWithoutSnapshot(t *testing.T) {
	repo, _ := newServerRepo(t)
	ctx := context.Background()
	// Solo llegó una membresía: todavía no hay datos del servidor.
	if err := repo.SetMembership(ctx, repository.Membership{ServerID: uid(1), UserID: uid(50), IsMember: true, OccurredAt: base}); err != nil {
		t.Fatal(err)
	}
	if _, total, err := repo.List(ctx, repository.ServerFilter{}, 10, 0); err != nil || total != 0 {
		t.Fatalf("total=%d err=%v", total, err)
	}
	// Mismo caso con la fila creada sin foto (server_deleted no es: ese la oculta).
	if err := repo.UpsertServer(ctx, snap(1, "Ya llegó", 9, 0, 1)); err != nil {
		t.Fatal(err)
	}
	got, _, err := repo.List(ctx, repository.ServerFilter{}, 10, 0)
	if err != nil || len(got) != 1 || got[0].MemberCount != 1 {
		t.Fatalf("la membresía previa debe contarse al llegar la foto: %+v err=%v", got, err)
	}
}

func TestUpsertServerStoresTheIconOfTheNewestSnapshot(t *testing.T) {
	repo, _ := newServerRepo(t)
	ctx := context.Background()
	withIcon := func(s repository.ServerSnapshot, icon string) repository.ServerSnapshot {
		s.IconURL = icon
		return s
	}
	steps := []struct {
		s    repository.ServerSnapshot
		want string
	}{
		{snap(1, "Sala", 9, 0, 1), ""},
		{withIcon(snap(1, "Sala", 9, 0, 10), "/v1/servers/x/icon?v=2"), "/v1/servers/x/icon?v=2"},
		{withIcon(snap(1, "Sala", 9, 0, 5), "/v1/servers/x/icon?v=1"), "/v1/servers/x/icon?v=2"}, // llegó tarde: no pisa
		{snap(1, "Sala", 9, 0, 20), ""}, // sacó el ícono
	}
	for i, st := range steps {
		if err := repo.UpsertServer(ctx, st.s); err != nil {
			t.Fatal(err)
		}
		got, _, err := repo.List(ctx, repository.ServerFilter{}, 10, 0)
		if err != nil || len(got) != 1 {
			t.Fatalf("paso %d: %+v err=%v", i, got, err)
		}
		icon := got[0].IconURL
		if st.want == "" && icon != nil || st.want != "" && (icon == nil || *icon != st.want) {
			t.Fatalf("paso %d: icon_url = %v, quería %q", i, icon, st.want)
		}
	}
}
