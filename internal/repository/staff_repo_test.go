package repository_test

import (
	"context"
	"testing"

	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/admin/internal/testutil"
)

const (
	staffA = "11111111-1111-1111-1111-111111111111"
	staffB = "22222222-2222-2222-2222-222222222222"
)

func TestStaffRepository(t *testing.T) {
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "staff_members")
	repo := repository.NewStaffRepository(db)
	ctx := context.Background()

	if ok, err := repo.IsStaff(ctx, staffA); err != nil || ok {
		t.Fatalf("antes de sembrar: ok=%v err=%v", ok, err)
	}

	if err := repo.AddAll(ctx, []string{staffA, staffB}); err != nil {
		t.Fatalf("AddAll: %v", err)
	}
	// Sembrar de nuevo (cada arranque) es idempotente.
	if err := repo.AddAll(ctx, []string{staffA}); err != nil {
		t.Fatalf("AddAll repetido: %v", err)
	}

	for _, id := range []string{staffA, staffB} {
		if ok, err := repo.IsStaff(ctx, id); err != nil || !ok {
			t.Errorf("%s: ok=%v err=%v", id, ok, err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM staff_members`).Scan(&count); err != nil || count != 2 {
		t.Errorf("count=%d err=%v, quería 2 sin duplicados", count, err)
	}
}

func TestAddAllRejectsInvalidUUID(t *testing.T) {
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "staff_members")
	repo := repository.NewStaffRepository(db)

	if err := repo.AddAll(context.Background(), []string{staffA, "no-es-uuid"}); err == nil {
		t.Fatal("esperaba error por un UUID inválido")
	}
	// La transacción hace rollback: tampoco queda el válido.
	if ok, _ := repo.IsStaff(context.Background(), staffA); ok {
		t.Error("AddAll debe ser atómico: no debería haber quedado ningún staff")
	}
}
