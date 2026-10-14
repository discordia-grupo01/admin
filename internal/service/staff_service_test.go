package service

import (
	"context"
	"errors"
	"testing"
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

func TestSeedWrapsRepositoryError(t *testing.T) {
	boom := errors.New("boom")
	err := NewStaffService(&fakeStaffRepo{err: boom}).Seed(context.Background(), []string{"a"})
	if !errors.Is(err, boom) {
		t.Fatalf("esperaba el error de la base envuelto, obtuve %v", err)
	}
}
