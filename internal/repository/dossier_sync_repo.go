package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ReportSnapshot es la foto completa de un reporte (servers.report_updated).
// ReportedUserID, ChannelID y MessageID vacíos se guardan como NULL.
type ReportSnapshot struct {
	ReportID       string
	ServerID       string
	Kind           string
	ReportedUserID string
	ChannelID      string
	MessageID      string
	Reason         string
	Status         string
	CreatedAt      time.Time
	ResolvedAt     *time.Time
	OccurredAt     time.Time
}

// ServerModerationAction es una acción de un moderador de un servidor
// (servers.moderation_action_recorded).
type ServerModerationAction struct {
	ActionID     string
	ServerID     string
	Kind         string
	TargetUserID string
	OccurredAt   time.Time
}

// DossierSyncRepository aplica a la réplica los eventos de servers que alimentan
// el expediente. Los dos descartan lo que llega de un servidor ya borrado: así
// un evento tardío no resucita datos que server_deleted purgó.
type DossierSyncRepository interface {
	// UpsertReport guarda la foto solo si es más nueva que la guardada.
	UpsertReport(ctx context.Context, r ReportSnapshot) error
	// InsertServerAction guarda la acción; repetirla no hace nada.
	InsertServerAction(ctx context.Context, a ServerModerationAction) error
}

type postgresDossierSyncRepository struct {
	db *sql.DB
}

func NewDossierSyncRepository(db *sql.DB) DossierSyncRepository {
	return &postgresDossierSyncRepository{db: db}
}

// serverNotDeleted es verdadero salvo que admin tenga el servidor marcado como
// borrado. Un servidor desconocido para admin sí se acepta: la fila es del
// reporte o de la acción, no del servidor.
const serverNotDeleted = `NOT EXISTS (SELECT 1 FROM servers WHERE server_id = %s AND deleted_at IS NOT NULL)`

func (r *postgresDossierSyncRepository) UpsertReport(ctx context.Context, s ReportSnapshot) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO reports (report_id, server_id, kind, reported_user_id, channel_id, message_id,
		                     reason, status, created_at, resolved_at, snapshot_at)
		SELECT $1::uuid, $2::uuid, $3::text, NULLIF($4::text, '')::uuid, NULLIF($5::text, '')::uuid,
		       NULLIF($6::text, '')::uuid, $7::text, $8::text, $9::timestamptz, $10::timestamptz, $11::timestamptz
		 WHERE `+fmt.Sprintf(serverNotDeleted, "$2::uuid")+`
		ON CONFLICT (report_id) DO UPDATE SET
			kind             = EXCLUDED.kind,
			reported_user_id = EXCLUDED.reported_user_id,
			channel_id       = EXCLUDED.channel_id,
			message_id       = EXCLUDED.message_id,
			reason           = EXCLUDED.reason,
			status           = EXCLUDED.status,
			created_at       = EXCLUDED.created_at,
			resolved_at      = EXCLUDED.resolved_at,
			snapshot_at      = EXCLUDED.snapshot_at
		WHERE reports.snapshot_at < EXCLUDED.snapshot_at`,
		s.ReportID, s.ServerID, s.Kind, s.ReportedUserID, s.ChannelID, s.MessageID,
		s.Reason, s.Status, s.CreatedAt.UTC(), s.ResolvedAt, s.OccurredAt.UTC())
	if err != nil {
		return fmt.Errorf("guardar el reporte %s: %w", s.ReportID, err)
	}
	return nil
}

func (r *postgresDossierSyncRepository) InsertServerAction(ctx context.Context, a ServerModerationAction) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO moderation_actions (id, scope, kind, target_user_id, server_id, occurred_at)
		SELECT $1::uuid, 'server', $2::text, $3::uuid, $4::uuid, $5::timestamptz
		 WHERE `+fmt.Sprintf(serverNotDeleted, "$4::uuid")+`
		ON CONFLICT (id) DO NOTHING`,
		a.ActionID, a.Kind, a.TargetUserID, a.ServerID, a.OccurredAt.UTC())
	if err != nil {
		return fmt.Errorf("guardar la acción %s: %w", a.ActionID, err)
	}
	return nil
}
