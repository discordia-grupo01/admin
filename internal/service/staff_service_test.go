package service

import (
	"context"
	"errors"
	"testing"

	"github.com/discordia-grupo01/admin/internal/repository"
)

type fakeStaffRepo struct {
	staff    map[string]bool
	err      error
	addCalls int
	added    []string
}

func (f *fakeStaffRepo) IsStaff(_ context.Context, id string) (bool, error) {
	return f.staff[id], f.err
}

func (f *fakeStaffRepo) AddAll(_ context.Context, ids []string) error {
	f.addCalls++
	f.added = append(f.added, ids...)
	return f.err
}

func TestIsStaffDelegates(t *testing.T) {
	svc := NewStaffService(&fakeStaffRepo{staff: map[string]bool{"a": true}})
	if ok, err := svc.IsStaff(context.Background(), "a"); err != nil || !ok {
		t.Errorf("a: ok=%v err=%v", ok, err)
	}
	if ok, err := svc.IsStaff(context.Background(), "b"); err != nil || ok {
		t.Errorf("b: ok=%v err=%v", ok, err)
	}
}

func TestSeedWithoutIDsDoesNothing(t *testing.T) {
	repo := &fakeStaffRepo{}
	if err := NewStaffService(repo).Seed(context.Background(), nil); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if repo.addCalls != 0 {
		t.Errorf("no debería tocar la base sin ids, llamadas: %d", repo.addCalls)
	}
}

func TestSeedAddsIDs(t *testing.T) {
	repo := &fakeStaffRepo{}
	if err := NewStaffService(repo).Seed(context.Background(), []string{"a", "b"}); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if len(repo.added) != 2 {
		t.Errorf("added = %v", repo.added)
	}
}

type fakeLookup struct {
	id    string
	err   error
	asked string
}

func (f *fakeLookup) FindIDByEmail(_ context.Context, email string) (string, error) {
	f.asked = email
	return f.id, f.err
}

func TestSeedByEmailAddsTheUserFound(t *testing.T) {
	repo := &fakeStaffRepo{}
	lookup := &fakeLookup{id: "11111111-1111-1111-1111-111111111111"}

	id, err := NewStaffService(repo).SeedByEmail(context.Background(), lookup, "  Manu@Example.com ")
	if err != nil {
		t.Fatal(err)
	}
	if id != lookup.id || len(repo.added) != 1 || repo.added[0] != lookup.id {
		t.Errorf("id=%q added=%v", id, repo.added)
	}
	if lookup.asked != "Manu@Example.com" {
		t.Errorf("el email debe buscarse sin espacios alrededor: %q", lookup.asked)
	}
}

func TestSeedByEmailUnknownUserSeedsNobody(t *testing.T) {
	repo := &fakeStaffRepo{}
	_, err := NewStaffService(repo).SeedByEmail(context.Background(), &fakeLookup{err: repository.ErrUserNotFound}, "nadie@example.com")
	if !errors.Is(err, repository.ErrUserNotFound) {
		t.Fatalf("err = %v, quería ErrUserNotFound", err)
	}
	if repo.addCalls != 0 {
		t.Error("si el usuario no existe no se debe sembrar ningún staff")
	}
}

func TestSeedByEmailRejectsMalformedEmailWithoutLookingItUp(t *testing.T) {
	for _, email := range []string{"", "   ", "sin-arroba"} {
		lookup := &fakeLookup{id: "x"}
		if _, err := NewStaffService(&fakeStaffRepo{}).SeedByEmail(context.Background(), lookup, email); err == nil {
			t.Errorf("%q debería fallar", email)
		}
		if lookup.asked != "" {
			t.Errorf("%q: no debería haber buscado nada", email)
		}
	}
}

func TestSeedByEmailPropagatesStaffRepositoryError(t *testing.T) {
	boom := errors.New("boom")
	_, err := NewStaffService(&fakeStaffRepo{err: boom}).SeedByEmail(context.Background(), &fakeLookup{id: "x"}, "a@b.c")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestSeedWrapsRepositoryError(t *testing.T) {
	boom := errors.New("boom")
	err := NewStaffService(&fakeStaffRepo{err: boom}).Seed(context.Background(), []string{"a"})
	if !errors.Is(err, boom) {
		t.Fatalf("esperaba el error de la base envuelto, obtuve %v", err)
	}
}
