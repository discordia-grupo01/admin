package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// MessageActivity es un mensaje enviado en un canal de un servidor. No tiene
// el texto: admin nunca lo guarda.
type MessageActivity struct {
	EventID     string
	UserID      string
	ServerID    string
	ChannelID   string
	ChannelName string // vacío = no vino en el evento
	MessageID   string
	OccurredAt  time.Time
}

type ActivityRepository interface {
	// InsertMessage guarda el mensaje enviado. Repetir el mismo evento no hace
	// nada, y el de un servidor ya borrado se descarta.
	InsertMessage(ctx context.Context, m MessageActivity) error
	// Prune borra la actividad anterior a before y devuelve cuántas filas.
	Prune(ctx context.Context, before time.Time) (int64, error)
}

type postgresActivityRepository struct {
	db *sql.DB
}

func NewActivityRepository(db *sql.DB) ActivityRepository {
	return &postgresActivityRepository{db: db}
}

func (r *postgresActivityRepository) InsertMessage(ctx context.Context, m MessageActivity) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_activity (id, user_id, kind, server_id, channel_id, channel_name, message_id, occurred_at)
		SELECT $1::uuid, $2::uuid, 'message_sent', $3::uuid, $4::uuid, NULLIF($5::text, ''), $6::uuid, $7::timestamptz
		 WHERE NOT EXISTS (SELECT 1 FROM servers WHERE server_id = $3::uuid AND deleted_at IS NOT NULL)
		ON CONFLICT (id) DO NOTHING`,
		m.EventID, m.UserID, m.ServerID, m.ChannelID, m.ChannelName, m.MessageID, m.OccurredAt.UTC())
	if err != nil {
		return fmt.Errorf("guardar la actividad del mensaje %s: %w", m.MessageID, err)
	}
	return nil
}

func (r *postgresActivityRepository) Prune(ctx context.Context, before time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM user_activity WHERE occurred_at < $1`, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("borrar la actividad anterior a %s: %w", before.UTC().Format(time.RFC3339), err)
	}
	return res.RowsAffected()
}
