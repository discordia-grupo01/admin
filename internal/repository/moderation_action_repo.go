package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Tipos de acción de plataforma que admin registra en moderation_actions.
const (
	ActionSuspension        = "suspension"
	ActionReactivation      = "reactivation"
	ActionDeletionRequested = "deletion_requested"
)

// platformAction es una acción del staff sobre un usuario o un servidor.
type platformAction struct {
	Kind         string
	TargetUserID string // vacío si la acción es sobre un servidor
	ServerID     string // vacío si la acción es sobre un usuario
	ActorID      string // vacío si no se conoce (la reactivación no lo registra)
	// At es el momento de la acción. Cero = el de la transacción (now()).
	At time.Time
}

// insertPlatformAction guarda la acción en el historial dentro de tx, así
// queda junto con el cambio de estado y su evento o no queda ninguno.
func insertPlatformAction(ctx context.Context, tx *sql.Tx, a platformAction) error {
	var at any
	if !a.At.IsZero() {
		at = a.At.UTC()
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO moderation_actions (id, scope, kind, target_user_id, server_id, actor_id, occurred_at)
		VALUES ($1, 'platform', $2, NULLIF($3, '')::uuid, NULLIF($4, '')::uuid, NULLIF($5, '')::uuid,
		        COALESCE($6::timestamptz, now()))`,
		uuid.NewString(), a.Kind, a.TargetUserID, a.ServerID, a.ActorID, at,
	); err != nil {
		return fmt.Errorf("registrar la acción %s en el historial: %w", a.Kind, err)
	}
	return nil
}
