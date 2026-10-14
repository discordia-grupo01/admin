// Package audit registra cada evento consumido en consumed_events, con su
// resultado. No participa de la idempotencia (esa la da cada handler con
// versiones): sirve para saber qué se descartó y por qué.
package audit

import (
	"context"
	"database/sql"
	"log"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusProcessed                 Status = "processed"
	StatusPending                   Status = "pending"
	StatusDiscardedMalformed        Status = "discarded_malformed_payload"
	StatusDiscardedUnknownKey       Status = "discarded_unknown_routing_key"
	StatusDiscardedInvalid          Status = "discarded_invalid_payload"
	StatusDiscardedInvalidSignature Status = "discarded_invalid_signature"
)

type Entry struct {
	EventID    string
	RoutingKey string
	RawBody    []byte
	Status     Status
	Err        error
}

// Recorder guarda el resultado de procesar un evento. Nunca devuelve error:
// una falla de auditoría no debe cambiar si el evento se confirma o se reencola.
type Recorder interface {
	Record(ctx context.Context, e Entry)
}

type PostgresRecorder struct {
	db *sql.DB
}

func NewPostgresRecorder(db *sql.DB) *PostgresRecorder {
	return &PostgresRecorder{db: db}
}

func (r *PostgresRecorder) Record(ctx context.Context, e Entry) {
	var errText *string
	if e.Err != nil {
		s := e.Err.Error()
		errText = &s
	}

	if e.EventID == "" {
		if _, err := r.db.ExecContext(ctx, `
			INSERT INTO consumed_events (id, event_id, routing_key, raw_body, status, processed_at, error)
			VALUES ($1, NULL, $2, $3, $4, now(), $5)`,
			uuid.NewString(), e.RoutingKey, string(e.RawBody), string(e.Status), errText,
		); err != nil {
			log.Printf("audit: no se pudo registrar la entrega de %q: %v", e.RoutingKey, err)
		}
		return
	}

	var processedAt *time.Time
	if e.Status != StatusPending {
		now := time.Now().UTC()
		processedAt = &now
	}

	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO consumed_events (id, event_id, routing_key, raw_body, status, processed_at, error)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (event_id) WHERE event_id IS NOT NULL DO UPDATE SET
			status       = EXCLUDED.status,
			attempts     = consumed_events.attempts + 1,
			processed_at = EXCLUDED.processed_at,
			error        = EXCLUDED.error`,
		uuid.NewString(), e.EventID, e.RoutingKey, string(e.RawBody), string(e.Status), processedAt, errText,
	); err != nil {
		log.Printf("audit: no se pudo registrar event_id=%s: %v", e.EventID, err)
	}
}
