package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/admin/internal/testutil"
)

func noEnv(string) string { return "" }

func TestRunRequiresEmail(t *testing.T) {
	for _, args := range [][]string{nil, {"--email", ""}, {"--email", "   "}} {
		err := run(context.Background(), args, noEnv, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "--email") {
			t.Errorf("args %v: esperaba un error pidiendo --email, obtuve %v", args, err)
		}
	}
}

func TestRunRejectsUnknownFlags(t *testing.T) {
	err := run(context.Background(), []string{"--nope"}, noEnv, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "argumentos inválidos") {
		t.Errorf("err = %v", err)
	}
}

func TestRunRejectsInvalidDatabaseConfigBeforeConnecting(t *testing.T) {
	getenv := func(k string) string {
		if k == "DATABASE_SCHEMA" {
			return "admin; DROP TABLE x"
		}
		return ""
	}
	err := run(context.Background(), []string{"--email", "a@b.c"}, getenv, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "DATABASE_SCHEMA") {
		t.Errorf("err = %v", err)
	}
}

// Camino completo contra Postgres: el usuario está en la réplica, se lo da de
// alta como staff y a partir de ahí el middleware lo reconoce. Repetirlo es inofensivo.
func TestRunSeedsStaffByEmail(t *testing.T) {
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "users", "staff_members")
	const id = "7c1e5a3b-9d2f-4b8e-a6c4-3f1e9d7b5a2c"
	if _, err := db.Exec(`INSERT INTO users (user_id, name, email, registered_at) VALUES ($1, 'Manu', 'manu@example.com', $2)`,
		id, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	getenv := os.Getenv // testutil.OpenDB ya validó que DATABASE_* apunta a una base de test

	for i := range 2 {
		var out bytes.Buffer
		if err := run(context.Background(), []string{"--email", " MANU@example.com "}, getenv, &out); err != nil {
			t.Fatalf("corrida %d: %v", i+1, err)
		}
		if !strings.Contains(out.String(), id) {
			t.Errorf("la salida debe mostrar el id: %q", out.String())
		}
	}

	staff := repository.NewStaffRepository(db)
	if ok, err := staff.IsStaff(context.Background(), id); err != nil || !ok {
		t.Fatalf("IsStaff = %v, %v", ok, err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM staff_members`).Scan(&n); err != nil || n != 1 {
		t.Errorf("filas en staff_members = %d (err %v), quería 1 aunque se corra dos veces", n, err)
	}
}

func TestRunUnknownEmailExplainsWhatToDo(t *testing.T) {
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "users", "staff_members")

	err := run(context.Background(), []string{"--email", "nadie@example.com"}, os.Getenv, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "STAFF_USER_IDS") {
		t.Fatalf("el error debe orientar al usuario (réplica / STAFF_USER_IDS): %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM staff_members`).Scan(&n); err != nil || n != 0 {
		t.Errorf("no debe sembrar a nadie: n=%d err=%v", n, err)
	}
}
