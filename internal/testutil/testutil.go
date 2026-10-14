// Package testutil tiene helpers para los tests de integración. Esos tests se
// saltean solos si no hay servicios configurados, así `go test ./...` anda sin
// Postgres ni RabbitMQ; en CI y con docker-compose sí corren.
package testutil

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/discordia-grupo01/admin/internal/config"
	_ "github.com/lib/pq"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// OpenDB abre la base de test. Se saltea si DATABASE_HOST no está definido.
// Como los tests vacían tablas, solo corre contra una base cuyo nombre
// contenga "test" o dentro de CI (GitHub Actions define CI=true): nunca contra
// una base de desarrollo. Las migraciones tienen que estar aplicadas.
func OpenDB(t *testing.T) *sql.DB {
	t.Helper()
	if os.Getenv("DATABASE_HOST") == "" {
		t.Skip("DATABASE_HOST no definido: se saltea el test de integración con Postgres")
	}
	cfg := config.Database{
		Host:     os.Getenv("DATABASE_HOST"),
		Port:     env("DATABASE_PORT", "5432"),
		Name:     env("DATABASE_NAME", "discordia_admin"),
		User:     env("DATABASE_USER", "discordia"),
		Password: os.Getenv("DATABASE_PASSWORD"),
		SSLMode:  env("DATABASE_SSLMODE", "disable"),
		Schema:   os.Getenv("DATABASE_SCHEMA"),
	}
	if !strings.Contains(cfg.Name, "test") && os.Getenv("CI") != "true" {
		t.Skipf("la base %q no parece de test (el nombre debe contener \"test\"): se saltea para no vaciar tablas de desarrollo", cfg.Name)
	}

	db, err := sql.Open("postgres", cfg.DSN())
	if err != nil {
		t.Fatalf("abrir la base de test: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("la base de test no responde: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// `go test ./...` corre los paquetes en paralelo y todos comparten la misma
	// base: sin esto un test vacía tablas que otro está usando. El lock de
	// sesión serializa los tests de integración, sin importar los flags (-p).
	// Se libera al terminar el test, después de que se limpien las tablas
	// (los Cleanup corren en orden inverso).
	lock, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("tomar una conexión para el lock: %v", err)
	}
	if _, err := lock.ExecContext(context.Background(), `SELECT pg_advisory_lock($1)`, advisoryLockID); err != nil {
		t.Fatalf("tomar el lock de tests de integración: %v", err)
	}
	t.Cleanup(func() {
		_, _ = lock.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryLockID)
		_ = lock.Close()
	})
	return db
}

// advisoryLockID identifica el lock que serializa los tests de integración.
const advisoryLockID = 727001

// Truncate vacía las tablas indicadas y vuelve a vaciarlas al terminar el test.
func Truncate(t *testing.T, db *sql.DB, tables ...string) {
	t.Helper()
	clean := func() {
		for _, table := range tables {
			if _, err := db.Exec("TRUNCATE TABLE " + table); err != nil {
				t.Fatalf("vaciar %s (¿migraciones aplicadas?): %v", table, err)
			}
		}
	}
	clean()
	t.Cleanup(clean)
}

// RabbitMQURL devuelve RABBITMQ_URL o saltea el test.
func RabbitMQURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("RABBITMQ_URL")
	if url == "" {
		t.Skip("RABBITMQ_URL no definido: se saltea el test de integración con RabbitMQ")
	}
	return url
}
