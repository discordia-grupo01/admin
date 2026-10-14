package outbox

import (
	"context"
	"database/sql"
	"fmt"
)

// Store toma lotes de eventos pendientes y los entrega a un callback.
type Store interface {
	// Dispatch toma hasta limit eventos pendientes, del más viejo al más nuevo,
	// y llama a publish con cada uno. Si publish devuelve nil, el evento queda
	// marcado como publicado; si devuelve error, se registra el intento y se
	// frena el lote (el evento sigue pendiente). Devuelve cuántos se publicaron.
	Dispatch(ctx context.Context, limit int, publish func(context.Context, Event) error) (int, error)
}

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// Dispatch usa FOR UPDATE SKIP LOCKED: si hay varias instancias del servicio,
// cada una toma eventos distintos y ninguno se publica dos veces a la vez.
func (s *PostgresStore) Dispatch(ctx context.Context, limit int, publish func(context.Context, Event) error) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
		SELECT id, routing_key, payload
		  FROM outbox_events
		 WHERE published_at IS NULL
		 ORDER BY created_at, id
		 LIMIT $1
		 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return 0, fmt.Errorf("leer eventos pendientes: %w", err)
	}
	var pending []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.RoutingKey, &e.Payload); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("leer un evento pendiente: %w", err)
		}
		pending = append(pending, e)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	_ = rows.Close()

	published := 0
	for _, e := range pending {
		if pubErr := publish(ctx, e); pubErr != nil {
			if _, err := tx.ExecContext(ctx, `
				UPDATE outbox_events SET attempts = attempts + 1, last_error = $2 WHERE id = $1`,
				e.ID, pubErr.Error()); err != nil {
				return published, fmt.Errorf("registrar el intento fallido de %s: %w", e.ID, err)
			}
			break
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE outbox_events SET published_at = now(), last_error = NULL WHERE id = $1`, e.ID); err != nil {
			return published, fmt.Errorf("marcar %s como publicado: %w", e.ID, err)
		}
		published++
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return published, nil
}
