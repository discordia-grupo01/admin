// Package audit registra cada evento consumido en consumed_events, con su
// resultado. No participa de la idempotencia (esa la da cada handler con
// versiones): sirve para saber qué se descartó y por qué.
package audit

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"

	"github.com/discordia-grupo01/admin/internal/logging"
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

// bodyless envuelve un Recorder para eventos cuyo cuerpo no debe quedar en
// ninguna tabla de admin (por ejemplo messaging.message_created, que trae un
// extracto del texto del mensaje).
type bodyless struct {
	next Recorder
}

// WithoutBody devuelve un Recorder que no guarda el cuerpo de los eventos y
// tampoco registra los procesados: su idempotencia la da el handler, y una fila
// por mensaje en consumed_events no aportaría nada. Sí registra los descartes y
// los pendientes, para no perder de vista qué falló, pero sin cuerpo.
func WithoutBody(next Recorder) Recorder {
	return &bodyless{next: next}
}

func (b *bodyless) Record(ctx context.Context, e Entry) {
	if e.Status == StatusProcessed {
		return
	}
	e.RawBody = nil
	b.next.Record(ctx, e)
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
			logging.Errorf("audit: no se pudo registrar la entrega de %q: %v", e.RoutingKey, err)
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
		logging.Errorf("audit: no se pudo registrar event_id=%s: %v", e.EventID, err)
	}
}
