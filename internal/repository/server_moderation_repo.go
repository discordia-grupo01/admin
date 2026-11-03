package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/discordia-grupo01/admin/internal/outbox"
)

var (
	// ErrServerNotFound indica que el servidor no existe para admin: no hay fila,
	// todavía no llegó su alta o ya fue borrado.
	ErrServerNotFound = errors.New("el servidor no existe")
	// ErrDeleteConfirmationMismatch indica que el texto de confirmación no es el
	// nombre del servidor.
	ErrDeleteConfirmationMismatch = errors.New("la confirmación no coincide con el nombre del servidor")
)

type ServerModerationRepository interface {
	// RequestDeletion registra el pedido de eliminación y escribe el evento en el
	// outbox, todo en una misma transacción: o quedan las dos cosas o ninguna.
	// Bloquea la fila del servidor, así que pedidos simultáneos reciben versiones
	// distintas.
	//
	// No borra nada: el servidor sale del listado cuando servers confirma con
	// servers.server_deleted (ServerRepository.MarkDeleted). Por eso se puede
	// repetir: es la forma de reintentar si el evento se perdió.
	//
	// confirmName se compara, sin espacios en los bordes, con el nombre que tiene
	// la réplica. Devuelve ErrServerNotFound si el servidor no existe para admin y
	// ErrDeleteConfirmationMismatch si el nombre no coincide; en esos casos no
	// cambia nada ni se escribe ningún evento.
	RequestDeletion(ctx context.Context, serverID, confirmName, staffID string, at time.Time, event EventBuilder) error
}

type postgresServerModerationRepository struct {
	db *sql.DB
}

func NewServerModerationRepository(db *sql.DB) ServerModerationRepository {
	return &postgresServerModerationRepository{db: db}
}

func (r *postgresServerModerationRepository) RequestDeletion(ctx context.Context, serverID, confirmName, staffID string, at time.Time, event EventBuilder) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("pedir la eliminación de %s: %w", serverID, err)
	}
	defer func() { _ = tx.Rollback() }()

	name, version, err := lockServer(ctx, tx, serverID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(confirmName) != name {
		return ErrDeleteConfirmationMismatch
	}

	version++
	if _, err := tx.ExecContext(ctx, `
		UPDATE servers
		   SET moderation_version = $2, deletion_requested_at = $3, deletion_requested_by = $4,
		       updated_at = now()
		 WHERE server_id = $1`,
		serverID, version, at.UTC(), staffID); err != nil {
		return fmt.Errorf("registrar el pedido de eliminación de %s: %w", serverID, err)
	}

	key, payload := event(version)
	if err := outbox.Insert(ctx, tx, key, payload); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("pedir la eliminación de %s: %w", serverID, err)
	}
	return nil
}

// lockServer bloquea la fila del servidor hasta el fin de la transacción y
// devuelve su nombre y su versión de moderación. Solo ve servidores con foto
// recibida y sin borrar, que son los que el backoffice puede listar.
func lockServer(ctx context.Context, tx *sql.Tx, serverID string) (string, int64, error) {
	var name sql.NullString
	var version int64
	err := tx.QueryRowContext(ctx, `
		SELECT name, moderation_version FROM servers
		 WHERE server_id = $1 AND created_at IS NOT NULL AND deleted_at IS NULL
		   FOR UPDATE`, serverID).Scan(&name, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, ErrServerNotFound
	}
	if err != nil {
		return "", 0, fmt.Errorf("leer el servidor %s: %w", serverID, err)
	}
	return name.String, version, nil
}
