package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/outbox"
)

var (
	// ErrServerNotFound indica que el servidor no existe para admin: no hay fila,
	// todavía no llegó su alta o ya fue borrado.
	ErrServerNotFound = errors.New("el servidor no existe")
	// ErrDeleteConfirmationMismatch indica que el texto de confirmación no es el
	// nombre del servidor.
	ErrDeleteConfirmationMismatch = errors.New("la confirmación no coincide con el nombre del servidor")
	// ErrServerAlreadySuspended indica que el servidor ya estaba suspendido.
	ErrServerAlreadySuspended = errors.New("el servidor ya está suspendido")
	// ErrServerNotSuspended indica que se quiso reactivar un servidor que no estaba suspendido.
	ErrServerNotSuspended = errors.New("el servidor no está suspendido")
)

// SuspendedServer es un servidor suspendido vigente, con lo necesario para
// volver a publicar su evento.
type SuspendedServer struct {
	ServerID    string
	Version     int64
	SuspendedAt time.Time
}

type ServerModerationRepository interface {
	// RequestDeletion registra el pedido de eliminación y escribe el evento en el
	// outbox, todo en una misma transacción: o quedan las dos cosas o ninguna.
	// Bloquea la fila del servidor, así que pedidos simultáneos reciben versiones
	// distintas.
	//
	// No borra nada: el servidor sale del listado cuando servers confirma con
	// servers.server_deleted (ServerRepository.MarkDeleted). Por eso se puede
	// repetir: es la forma de reintentar si el evento se perdió. Funciona también
	// con un servidor suspendido.
	//
	// confirmName se compara, sin espacios en los bordes, con el nombre que tiene
	// la réplica. Devuelve ErrServerNotFound si el servidor no existe para admin y
	// ErrDeleteConfirmationMismatch si el nombre no coincide; en esos casos no
	// cambia nada ni se escribe ningún evento.
	RequestDeletion(ctx context.Context, serverID, confirmName, staffID string, at time.Time, event EventBuilder) error

	// Suspend suspende el servidor y escribe el evento en el outbox, todo en una
	// misma transacción. Bloquea la fila, así que dos suspensiones simultáneas no
	// pueden pasar las dos. Devuelve el servidor ya suspendido.
	//
	// Devuelve ErrServerNotFound si no existe para admin y ErrServerAlreadySuspended
	// si ya lo estaba; en esos casos no cambia nada ni se escribe evento.
	Suspend(ctx context.Context, serverID, staffID string, at time.Time, event EventBuilder) (*model.Server, error)

	// Reactivate levanta la suspensión y escribe el evento en el outbox, en una
	// misma transacción y con la misma garantía que Suspend. Devuelve
	// ErrServerNotFound si no existe para admin y ErrServerNotSuspended si no
	// estaba suspendido; en esos casos no cambia nada ni se escribe evento.
	Reactivate(ctx context.Context, serverID string, event EventBuilder) (*model.Server, error)

	// ListSuspended devuelve todos los servidores suspendidos que no fueron
	// borrados, con la versión vigente, del más viejo al más nuevo.
	ListSuspended(ctx context.Context) ([]SuspendedServer, error)
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

	lock, err := lockServer(ctx, tx, serverID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(confirmName) != lock.name {
		return ErrDeleteConfirmationMismatch
	}

	version := lock.version + 1
	if _, err := tx.ExecContext(ctx, `
		UPDATE servers
		   SET moderation_version = $2, deletion_requested_at = $3, deletion_requested_by = $4,
		       updated_at = now()
		 WHERE server_id = $1`,
		serverID, version, at.UTC(), staffID); err != nil {
		return fmt.Errorf("registrar el pedido de eliminación de %s: %w", serverID, err)
	}

	if err := insertPlatformAction(ctx, tx, platformAction{
		Kind: ActionDeletionRequested, ServerID: serverID, ActorID: staffID, At: at,
	}); err != nil {
		return err
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

func (r *postgresServerModerationRepository) Suspend(ctx context.Context, serverID, staffID string, at time.Time, event EventBuilder) (*model.Server, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("suspender el servidor %s: %w", serverID, err)
	}
	defer func() { _ = tx.Rollback() }()

	lock, err := lockServer(ctx, tx, serverID)
	if err != nil {
		return nil, err
	}
	if lock.suspended {
		return nil, ErrServerAlreadySuspended
	}

	version := lock.version + 1
	if _, err := tx.ExecContext(ctx, `
		UPDATE servers
		   SET suspended_at = $2, suspended_by = $3, moderation_version = $4, updated_at = now()
		 WHERE server_id = $1`,
		serverID, at.UTC(), staffID, version); err != nil {
		return nil, fmt.Errorf("suspender el servidor %s: %w", serverID, err)
	}
	s, err := getServer(ctx, tx, serverID)
	if err != nil {
		return nil, err
	}

	if err := insertPlatformAction(ctx, tx, platformAction{
		Kind: ActionSuspension, ServerID: serverID, ActorID: staffID, At: at,
	}); err != nil {
		return nil, err
	}

	key, payload := event(version)
	if err := outbox.Insert(ctx, tx, key, payload); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("suspender el servidor %s: %w", serverID, err)
	}
	return s, nil
}

func (r *postgresServerModerationRepository) Reactivate(ctx context.Context, serverID string, event EventBuilder) (*model.Server, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("reactivar el servidor %s: %w", serverID, err)
	}
	defer func() { _ = tx.Rollback() }()

	lock, err := lockServer(ctx, tx, serverID)
	if err != nil {
		return nil, err
	}
	if !lock.suspended {
		return nil, ErrServerNotSuspended
	}

	version := lock.version + 1
	if _, err := tx.ExecContext(ctx, `
		UPDATE servers
		   SET suspended_at = NULL, suspended_by = NULL, moderation_version = $2, updated_at = now()
		 WHERE server_id = $1`,
		serverID, version); err != nil {
		return nil, fmt.Errorf("reactivar el servidor %s: %w", serverID, err)
	}
	s, err := getServer(ctx, tx, serverID)
	if err != nil {
		return nil, err
	}

	if err := insertPlatformAction(ctx, tx, platformAction{Kind: ActionReactivation, ServerID: serverID}); err != nil {
		return nil, err
	}

	key, payload := event(version)
	if err := outbox.Insert(ctx, tx, key, payload); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("reactivar el servidor %s: %w", serverID, err)
	}
	return s, nil
}

func (r *postgresServerModerationRepository) ListSuspended(ctx context.Context) ([]SuspendedServer, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT server_id, moderation_version, suspended_at
		  FROM servers
		 WHERE suspended_at IS NOT NULL AND deleted_at IS NULL
		 ORDER BY suspended_at, server_id`)
	if err != nil {
		return nil, fmt.Errorf("listar servidores suspendidos: %w", err)
	}
	defer rows.Close()

	var out []SuspendedServer
	for rows.Next() {
		var s SuspendedServer
		if err := rows.Scan(&s.ServerID, &s.Version, &s.SuspendedAt); err != nil {
			return nil, fmt.Errorf("leer un servidor suspendido: %w", err)
		}
		s.SuspendedAt = s.SuspendedAt.UTC()
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// serverLock es lo que se lee al bloquear un servidor para moderarlo.
type serverLock struct {
	name      string
	version   int64
	suspended bool
}

// lockServer bloquea la fila del servidor hasta el fin de la transacción y
// devuelve su nombre, su versión de moderación y si está suspendido. Solo ve
// servidores con foto recibida y sin borrar, que son los que el backoffice puede
// listar.
func lockServer(ctx context.Context, tx *sql.Tx, serverID string) (serverLock, error) {
	var name sql.NullString
	var lock serverLock
	err := tx.QueryRowContext(ctx, `
		SELECT name, moderation_version, suspended_at IS NOT NULL FROM servers
		 WHERE server_id = $1 AND created_at IS NOT NULL AND deleted_at IS NULL
		   FOR UPDATE`, serverID).Scan(&name, &lock.version, &lock.suspended)
	if errors.Is(err, sql.ErrNoRows) {
		return serverLock{}, ErrServerNotFound
	}
	if err != nil {
		return serverLock{}, fmt.Errorf("leer el servidor %s: %w", serverID, err)
	}
	lock.name = name.String
	return lock, nil
}
