package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/testutil"
	"github.com/discordia-grupo01/events-contract/admin"
)

const secret = "secreto-de-prueba"

type spyPublisher struct {
	keys     []string
	payloads []any
	failAt   int // 1-based; 0 = nunca falla
	closed   bool
}

func (s *spyPublisher) Publish(_ context.Context, key string, payload any) error {
	if s.failAt > 0 && len(s.keys)+1 == s.failAt {
		return errors.New("broker caído")
	}
	s.keys = append(s.keys, key)
	s.payloads = append(s.payloads, payload)
	return nil
}

func (s *spyPublisher) Close() { s.closed = true }

// env toma la conexión a la base de os.Getenv (testutil.OpenDB ya validó que es
// una base de test) y agrega lo que el comando exige además.
func env(extra map[string]string) func(string) string {
	return func(k string) string {
		if v, ok := extra[k]; ok {
			return v
		}
		return os.Getenv(k)
	}
}

func neverPublisher(t *testing.T) func(string) publisher {
	return func(string) publisher {
		t.Error("no debería crearse un publisher")
		return &spyPublisher{}
	}
}

func TestRunRejectsUnknownFlags(t *testing.T) {
	err := run(context.Background(), []string{"--nope"}, func(string) string { return "" }, &bytes.Buffer{}, neverPublisher(t))
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
	err := run(context.Background(), nil, getenv, &bytes.Buffer{}, neverPublisher(t))
	if err == nil || !strings.Contains(err.Error(), "DATABASE_SCHEMA") {
		t.Errorf("err = %v", err)
	}
}

func TestRunRequiresBrokerAndSecret(t *testing.T) {
	cases := map[string]map[string]string{
		"sin RABBITMQ_URL":             {"RABBITMQ_URL": "", "ADMIN_EVENTS_HMAC_SECRET": secret},
		"sin ADMIN_EVENTS_HMAC_SECRET": {"RABBITMQ_URL": "amqp://x", "ADMIN_EVENTS_HMAC_SECRET": ""},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			err := run(context.Background(), nil, env(extra), &bytes.Buffer{}, neverPublisher(t))
			if err == nil || !strings.Contains(err.Error(), "faltan variables de entorno") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func seedSuspended(t *testing.T) {
	t.Helper()
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "users")
	registered := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	suspended := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	rows := []struct {
		id, name, email string
		status          string
		version         int64
	}{
		{"00000000-0000-0000-0000-000000000001", "Activo", "a@example.com", "active", 0},
		{"00000000-0000-0000-0000-000000000002", "Suspendido", "s@example.com", "suspended", 1},
		{"00000000-0000-0000-0000-000000000003", "Reincidente", "r@example.com", "suspended", 3},
	}
	for _, r := range rows {
		var at any
		if r.status == "suspended" {
			at = suspended
		}
		if _, err := db.Exec(`
			INSERT INTO users (user_id, name, email, registered_at, status, suspended_at, suspension_version)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			r.id, r.name, r.email, registered, r.status, at, r.version); err != nil {
			t.Fatal(err)
		}
	}
}

// Camino completo contra Postgres: un evento firmado por cada suspendido, con
// su versión vigente, y ninguno para los activos.
func TestRunRepublishesEverySuspension(t *testing.T) {
	seedSuspended(t)
	spy := &spyPublisher{}
	var out bytes.Buffer
	extra := map[string]string{"RABBITMQ_URL": "amqp://x", "ADMIN_EVENTS_HMAC_SECRET": secret}

	if err := run(context.Background(), nil, env(extra), &out, func(string) publisher { return spy }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "2 suspensión(es) re-publicada(s)") {
		t.Errorf("salida: %q", out.String())
	}
	if !spy.closed {
		t.Error("el publisher debe cerrarse")
	}
	if len(spy.payloads) != 2 {
		t.Fatalf("eventos publicados: %d", len(spy.payloads))
	}
	versions := map[string]int64{}
	for i, p := range spy.payloads {
		e, ok := p.(admin.UserSuspended)
		if !ok || spy.keys[i] != admin.RoutingKeyUserSuspended {
			t.Fatalf("evento %d: %T con clave %q", i, p, spy.keys[i])
		}
		if !e.Verify(secret) {
			t.Errorf("la firma de %s no verifica", e.UserID)
		}
		versions[e.UserID] = e.Version
	}
	if versions["00000000-0000-0000-0000-000000000002"] != 1 || versions["00000000-0000-0000-0000-000000000003"] != 3 {
		t.Errorf("versiones publicadas: %v", versions)
	}
}

func TestRunDryRunPublishesNothing(t *testing.T) {
	seedSuspended(t)
	var out bytes.Buffer
	extra := map[string]string{"RABBITMQ_URL": "amqp://x", "ADMIN_EVENTS_HMAC_SECRET": secret}

	if err := run(context.Background(), []string{"--dry-run"}, env(extra), &out, neverPublisher(t)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "[dry-run] hay 2 suspensión(es)") {
		t.Errorf("salida: %q", out.String())
	}
}

func TestRunWithNobodySuspendedSaysSo(t *testing.T) {
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "users")
	spy := &spyPublisher{}
	var out bytes.Buffer
	extra := map[string]string{"RABBITMQ_URL": "amqp://x", "ADMIN_EVENTS_HMAC_SECRET": secret}

	if err := run(context.Background(), nil, env(extra), &out, func(string) publisher { return spy }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "0 suspensión(es)") || len(spy.keys) != 0 {
		t.Errorf("salida=%q publicados=%d", out.String(), len(spy.keys))
	}
}

func TestRunFailsLoudlyWhenThePublishFails(t *testing.T) {
	seedSuspended(t)
	spy := &spyPublisher{failAt: 2}
	extra := map[string]string{"RABBITMQ_URL": "amqp://x", "ADMIN_EVENTS_HMAC_SECRET": secret}

	err := run(context.Background(), nil, env(extra), &bytes.Buffer{}, func(string) publisher { return spy })
	if err == nil || !strings.Contains(err.Error(), "se alcanzaron a publicar 1") || !strings.Contains(err.Error(), "broker caído") {
		t.Fatalf("err = %v", err)
	}
	if !spy.closed {
		t.Error("el publisher debe cerrarse también al fallar")
	}
}
