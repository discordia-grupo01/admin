// Package config carga y valida la configuración del servicio desde variables
// de entorno. Falla al arrancar (en vez de en runtime) si falta algo crítico:
// un secreto vacío o un UUID de staff inválido no deben pasar desapercibidos.
package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	EnvDevelopment = "development"
	EnvProduction  = "production"

	defaultOutboxPollInterval = 2 * time.Second
)

var schemaPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type Database struct {
	Host     string
	Port     string
	Name     string
	User     string
	Password string
	SSLMode  string
	// Schema fija el search_path de la sesión (ej. "admin" en el proyecto de
	// Supabase compartido). Vacío en local.
	Schema string
}

type Config struct {
	Environment string
	Host        string
	Port        string
	Database    Database
	JWTSecret   string
	RabbitMQURL string
	// AdminEventsHMACSecret firma los eventos admin.* (ver PLANNING.md).
	AdminEventsHMACSecret string
	// StaffUserIDs se siembran en staff_members al arrancar.
	StaffUserIDs       []string
	OutboxPollInterval time.Duration
}

// Addr es la dirección donde escucha el servidor HTTP.
func (c Config) Addr() string { return c.Host + ":" + c.Port }

// DSN arma la cadena de conexión de lib/pq. Los valores van entre comillas
// simples para tolerar espacios y caracteres especiales en la password.
func (d Database) DSN() string {
	parts := []string{
		"host=" + quote(d.Host),
		"port=" + quote(d.Port),
		"user=" + quote(d.User),
		"password=" + quote(d.Password),
		"dbname=" + quote(d.Name),
		"sslmode=" + quote(d.SSLMode),
	}
	if d.Schema != "" {
		parts = append(parts, "search_path="+quote(d.Schema))
	}
	return strings.Join(parts, " ")
}

func quote(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `'`, `\'`)
	return "'" + v + "'"
}

func getOr(getenv func(string) string, key, def string) string {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		return v
	}
	return def
}

func databaseFromEnv(getenv func(string) string) Database {
	return Database{
		Host:     getOr(getenv, "DATABASE_HOST", "localhost"),
		Port:     getOr(getenv, "DATABASE_PORT", "5432"),
		Name:     getOr(getenv, "DATABASE_NAME", "discordia_admin"),
		User:     getOr(getenv, "DATABASE_USER", "discordia"),
		Password: getenv("DATABASE_PASSWORD"),
		SSLMode:  getOr(getenv, "DATABASE_SSLMODE", "disable"),
		Schema:   getOr(getenv, "DATABASE_SCHEMA", ""),
	}
}

func (d Database) validate() error {
	if d.Schema != "" && !schemaPattern.MatchString(d.Schema) {
		return fmt.Errorf("DATABASE_SCHEMA %q inválido: solo letras, números y _", d.Schema)
	}
	return nil
}

// LoadDatabase lee solo la conexión a la base. Es para las herramientas de
// línea de comandos (cmd/seedstaff), que no necesitan los secretos del servicio
// (JWT, RabbitMQ, firma de eventos).
func LoadDatabase(getenv func(string) string) (Database, error) {
	db := databaseFromEnv(getenv)
	if err := db.validate(); err != nil {
		return Database{}, err
	}
	return db, nil
}

// Load lee la configuración con getenv (os.Getenv en producción; un map en los
// tests) y devuelve todos los errores de validación juntos.
func Load(getenv func(string) string) (Config, error) {
	get := func(key, def string) string { return getOr(getenv, key, def) }

	cfg := Config{
		Environment:           get("ENVIRONMENT", EnvDevelopment),
		Host:                  get("HOST", "0.0.0.0"),
		Port:                  get("PORT", "8080"),
		Database:              databaseFromEnv(getenv),
		JWTSecret:             getenv("JWT_SECRET"),
		RabbitMQURL:           getenv("RABBITMQ_URL"),
		AdminEventsHMACSecret: getenv("ADMIN_EVENTS_HMAC_SECRET"),
		OutboxPollInterval:    defaultOutboxPollInterval,
	}

	var errs []error

	if cfg.Environment != EnvDevelopment && cfg.Environment != EnvProduction {
		errs = append(errs, fmt.Errorf("ENVIRONMENT %q inválido: debe ser %q o %q", cfg.Environment, EnvDevelopment, EnvProduction))
	}
	if cfg.JWTSecret == "" {
		errs = append(errs, errors.New("JWT_SECRET es obligatorio"))
	}
	if cfg.RabbitMQURL == "" {
		errs = append(errs, errors.New("RABBITMQ_URL es obligatorio"))
	}
	if cfg.AdminEventsHMACSecret == "" {
		errs = append(errs, errors.New("ADMIN_EVENTS_HMAC_SECRET es obligatorio (firma de eventos admin.*)"))
	}
	if err := cfg.Database.validate(); err != nil {
		errs = append(errs, err)
	}

	if raw := strings.TrimSpace(getenv("OUTBOX_POLL_INTERVAL")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("OUTBOX_POLL_INTERVAL %q inválido: usar una duración positiva como 2s", raw))
		} else {
			cfg.OutboxPollInterval = d
		}
	}

	ids, err := parseStaffUserIDs(getenv("STAFF_USER_IDS"))
	if err != nil {
		errs = append(errs, err)
	}
	cfg.StaffUserIDs = ids

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return cfg, nil
}

func parseStaffUserIDs(raw string) ([]string, error) {
	var ids []string
	var errs []error
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := uuid.Parse(part)
		if err != nil {
			errs = append(errs, fmt.Errorf("STAFF_USER_IDS: %q no es un UUID válido", part))
			continue
		}
		s := id.String()
		if !seen[s] {
			seen[s] = true
			ids = append(ids, s)
		}
	}
	return ids, errors.Join(errs...)
}
