package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func validEnv() map[string]string {
	return map[string]string{
		"JWT_SECRET":               "jwt",
		"RABBITMQ_URL":             "amqp://guest:guest@localhost:5672/",
		"ADMIN_EVENTS_HMAC_SECRET": "hmac",
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(validEnv()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Environment != EnvDevelopment || cfg.Port != "8080" || cfg.Host != "0.0.0.0" {
		t.Errorf("defaults inesperados: %+v", cfg)
	}
	if cfg.Database.Name != "discordia_admin" || cfg.Database.SSLMode != "disable" || cfg.Database.Schema != "" {
		t.Errorf("defaults de base inesperados: %+v", cfg.Database)
	}
	if cfg.OutboxPollInterval != 2*time.Second {
		t.Errorf("OutboxPollInterval = %v, quería 2s", cfg.OutboxPollInterval)
	}
	if len(cfg.StaffUserIDs) != 0 {
		t.Errorf("StaffUserIDs debería estar vacío: %v", cfg.StaffUserIDs)
	}
	if cfg.Addr() != "0.0.0.0:8080" {
		t.Errorf("Addr = %q", cfg.Addr())
	}
}

func TestLoadRequiredSecrets(t *testing.T) {
	for _, missing := range []string{"JWT_SECRET", "RABBITMQ_URL", "ADMIN_EVENTS_HMAC_SECRET"} {
		t.Run(missing, func(t *testing.T) {
			m := validEnv()
			delete(m, missing)
			_, err := Load(env(m))
			if err == nil || !strings.Contains(err.Error(), missing) {
				t.Fatalf("esperaba error por %s faltante, obtuve %v", missing, err)
			}
		})
	}
}

func TestLoadAllErrorsTogether(t *testing.T) {
	_, err := Load(env(map[string]string{"ENVIRONMENT": "staging"}))
	if err == nil {
		t.Fatal("esperaba error")
	}
	for _, want := range []string{"ENVIRONMENT", "JWT_SECRET", "RABBITMQ_URL", "ADMIN_EVENTS_HMAC_SECRET"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("el error debería mencionar %s: %v", want, err)
		}
	}
}

func TestLoadStaffUserIDs(t *testing.T) {
	m := validEnv()
	m["STAFF_USER_IDS"] = " 11111111-1111-1111-1111-111111111111, 22222222-2222-2222-2222-222222222222 ,,11111111-1111-1111-1111-111111111111"
	cfg, err := Load(env(m))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.StaffUserIDs) != 2 {
		t.Fatalf("esperaba 2 ids sin duplicados, obtuve %v", cfg.StaffUserIDs)
	}
}

func TestLoadStaffUserIDsInvalid(t *testing.T) {
	m := validEnv()
	m["STAFF_USER_IDS"] = "no-es-uuid"
	_, err := Load(env(m))
	if err == nil || !strings.Contains(err.Error(), "no-es-uuid") {
		t.Fatalf("esperaba error mencionando el valor inválido, obtuve %v", err)
	}
}

func TestLoadOutboxPollInterval(t *testing.T) {
	m := validEnv()
	m["OUTBOX_POLL_INTERVAL"] = "500ms"
	cfg, err := Load(env(m))
	if err != nil || cfg.OutboxPollInterval != 500*time.Millisecond {
		t.Fatalf("intervalo válido: cfg=%v err=%v", cfg.OutboxPollInterval, err)
	}
	for _, bad := range []string{"abc", "0s", "-1s"} {
		m["OUTBOX_POLL_INTERVAL"] = bad
		if _, err := Load(env(m)); err == nil {
			t.Errorf("OUTBOX_POLL_INTERVAL=%q debería fallar", bad)
		}
	}
}

func TestLoadSchemaValidation(t *testing.T) {
	m := validEnv()
	m["DATABASE_SCHEMA"] = "admin"
	if _, err := Load(env(m)); err != nil {
		t.Fatalf("schema válido: %v", err)
	}
	m["DATABASE_SCHEMA"] = "admin; DROP TABLE x"
	if _, err := Load(env(m)); err == nil {
		t.Fatal("schema con caracteres peligrosos debería fallar")
	}
}

// LoadDatabase es para las herramientas de línea de comandos: no exige los
// secretos del servicio, pero valida la conexión igual que Load.
func TestLoadDatabaseDoesNotRequireServiceSecrets(t *testing.T) {
	db, err := LoadDatabase(env(map[string]string{"DATABASE_HOST": "db", "DATABASE_PORT": "5433", "DATABASE_SCHEMA": "admin"}))
	if err != nil {
		t.Fatalf("LoadDatabase: %v", err)
	}
	if db.Host != "db" || db.Port != "5433" || db.Name != "discordia_admin" || db.Schema != "admin" {
		t.Errorf("database = %+v", db)
	}
}

func TestLoadDatabaseRejectsInvalidSchema(t *testing.T) {
	if _, err := LoadDatabase(env(map[string]string{"DATABASE_SCHEMA": "admin; DROP TABLE x"})); err == nil {
		t.Fatal("un schema con caracteres peligrosos debería fallar")
	}
}

func TestDSNQuotesValues(t *testing.T) {
	d := Database{Host: "h", Port: "5432", Name: "db", User: "u", Password: `p a'ss\`, SSLMode: "require", Schema: "admin"}
	got := d.DSN()
	want := `host='h' port='5432' user='u' password='p a\'ss\\' dbname='db' sslmode='require' search_path='admin'`
	if got != want {
		t.Errorf("DSN:\n got %s\nwant %s", got, want)
	}
	d.Schema = ""
	if strings.Contains(d.DSN(), "search_path") {
		t.Error("sin schema no debe fijar search_path")
	}
}
