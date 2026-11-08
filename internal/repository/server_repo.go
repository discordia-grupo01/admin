package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/google/uuid"
)

// ServerFilter son los filtros del listado. Los campos vacíos no filtran.
type ServerFilter struct {
	// Search busca por nombre, parcial y sin distinguir mayúsculas.
	Search string
	// Status deja solo los servidores activos o solo los suspendidos.
	Status model.ServerStatus
}

// ServerSnapshot es la foto completa de un servidor (servers.server_updated).
type ServerSnapshot struct {
	ServerID   string
	Name       string
	OwnerID    string
	CreatedAt  time.Time
	OccurredAt time.Time
}

// Membership es el último estado conocido de un usuario en un servidor.
type Membership struct {
	// EventID es el event_id del evento de origen y el id de la actividad que se
	// registra. Si no es un UUID, la membresía se aplica igual pero sin actividad.
	EventID    string
	ServerID   string
	UserID     string
	IsMember   bool
	OccurredAt time.Time
}

type ServerRepository interface {
	// UpsertServer guarda la foto solo si es más nueva que la guardada y el
	// servidor no fue eliminado, así un evento repetido o fuera de orden no
	// pisa datos nuevos ni resucita un servidor borrado.
	UpsertServer(ctx context.Context, s ServerSnapshot) error
	// MarkDeleted oculta el servidor del listado y descarta sus membresías. Es
	// idempotente y funciona aunque la foto del servidor no haya llegado.
	MarkDeleted(ctx context.Context, serverID string, at time.Time) error
	// SetMembership registra si el usuario es miembro, solo si el evento es más
	// nuevo que el último que se aplicó para ese par.
	SetMembership(ctx context.Context, m Membership) error

	// List devuelve una página de servidores que cumplen el filtro, del más
	// reciente al más viejo, y cuántos hay en total. Solo incluye servidores con
	// foto recibida y sin borrar.
	List(ctx context.Context, filter ServerFilter, limit, offset int) ([]model.Server, int, error)
}

type postgresServerRepository struct {
	db *sql.DB
}

func NewServerRepository(db *sql.DB) ServerRepository {
	return &postgresServerRepository{db: db}
}

func (r *postgresServerRepository) UpsertServer(ctx context.Context, s ServerSnapshot) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO servers (server_id, name, owner_id, created_at, snapshot_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (server_id) DO UPDATE SET
			name        = EXCLUDED.name,
			owner_id    = EXCLUDED.owner_id,
			created_at  = EXCLUDED.created_at,
			snapshot_at = EXCLUDED.snapshot_at,
			updated_at  = now()
		WHERE servers.deleted_at IS NULL
		  AND (servers.snapshot_at IS NULL OR servers.snapshot_at < EXCLUDED.snapshot_at)`,
		s.ServerID, s.Name, s.OwnerID, s.CreatedAt, s.OccurredAt)
	if err != nil {
		return fmt.Errorf("guardar el servidor %s: %w", s.ServerID, err)
	}
	return nil
}

func (r *postgresServerRepository) MarkDeleted(ctx context.Context, serverID string, at time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("borrar el servidor %s: %w", serverID, err)
	}
	defer tx.Rollback() //nolint:errcheck // tras el Commit no hace nada

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO servers (server_id, deleted_at) VALUES ($1, $2)
		ON CONFLICT (server_id) DO UPDATE SET
			deleted_at = COALESCE(servers.deleted_at, EXCLUDED.deleted_at),
			updated_at = now()`, serverID, at); err != nil {
		return fmt.Errorf("marcar el servidor %s como borrado: %w", serverID, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM server_members WHERE server_id = $1`, serverID); err != nil {
		return fmt.Errorf("borrar las membresías de %s: %w", serverID, err)
	}
	// Servers borra físicamente todo lo del servidor; admin hace lo mismo con su
	// réplica. Las acciones de plataforma (suspensión, pedido de eliminación) se
	// conservan: son el registro de lo que hizo el staff, no datos del servidor.
	if _, err := tx.ExecContext(ctx, `DELETE FROM reports WHERE server_id = $1`, serverID); err != nil {
		return fmt.Errorf("borrar los reportes de %s: %w", serverID, err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM moderation_actions WHERE server_id = $1 AND scope = 'server'`, serverID); err != nil {
		return fmt.Errorf("borrar la moderación de %s: %w", serverID, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_activity WHERE server_id = $1`, serverID); err != nil {
		return fmt.Errorf("borrar la actividad de %s: %w", serverID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("borrar el servidor %s: %w", serverID, err)
	}
	return nil
}

func (r *postgresServerRepository) SetMembership(ctx context.Context, m Membership) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("guardar la membresía de %s en %s: %w", m.UserID, m.ServerID, err)
	}
	defer tx.Rollback() //nolint:errcheck // tras el Commit no hace nada

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO server_members (server_id, user_id, is_member, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (server_id, user_id) DO UPDATE SET
			is_member  = EXCLUDED.is_member,
			updated_at = EXCLUDED.updated_at
		WHERE server_members.updated_at < EXCLUDED.updated_at`,
		m.ServerID, m.UserID, m.IsMember, m.OccurredAt); err != nil {
		return fmt.Errorf("guardar la membresía de %s en %s: %w", m.UserID, m.ServerID, err)
	}

	// La actividad es un hecho inmutable: se registra aunque el evento sea más
	// viejo que el último aplicado, salvo que el servidor ya esté borrado (los
	// member_left del propio borrado no deben dejar nada).
	if id, err := uuid.Parse(m.EventID); err == nil {
		kind := "left_server"
		if m.IsMember {
			kind = "joined_server"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO user_activity (id, user_id, kind, server_id, occurred_at)
			SELECT $1::uuid, $2::uuid, $3::text, $4::uuid, $5::timestamptz
			 WHERE NOT EXISTS (SELECT 1 FROM servers WHERE server_id = $4::uuid AND deleted_at IS NOT NULL)
			ON CONFLICT (id) DO NOTHING`,
			id.String(), m.UserID, kind, m.ServerID, m.OccurredAt.UTC()); err != nil {
			return fmt.Errorf("registrar la actividad de %s en %s: %w", m.UserID, m.ServerID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("guardar la membresía de %s en %s: %w", m.UserID, m.ServerID, err)
	}
	return nil
}

// El estado se deriva de suspended_at: un servidor está suspendido si lo tiene.
const serverFilterClause = `
	created_at IS NOT NULL
	AND deleted_at IS NULL
	AND ($1 = '' OR name ILIKE $2 ESCAPE '\')
	AND ($3 = '' OR ($3 = 'suspended') = (suspended_at IS NOT NULL))`

// serverColumns son las columnas de un servidor tal como lo ve el backoffice,
// sobre `servers s LEFT JOIN users u` (u es el dueño).
const serverColumns = `s.server_id, s.name, s.owner_id, u.name, s.created_at,
	(SELECT count(*) FROM server_members m WHERE m.server_id = s.server_id AND m.is_member),
	s.suspended_at`

// scanServer lee una fila con las columnas de serverColumns.
func scanServer(row interface{ Scan(dest ...any) error }) (model.Server, error) {
	var s model.Server
	var name, ownerID, ownerName sql.NullString
	var suspendedAt sql.NullTime
	if err := row.Scan(&s.ID, &name, &ownerID, &ownerName, &s.CreatedAt, &s.MemberCount, &suspendedAt); err != nil {
		return model.Server{}, err
	}
	s.Name, s.OwnerID = name.String, ownerID.String
	if ownerName.Valid {
		s.OwnerName = &ownerName.String
	}
	s.CreatedAt = s.CreatedAt.UTC()
	if suspendedAt.Valid {
		at := suspendedAt.Time.UTC()
		s.SuspendedAt = &at
	}
	return s, nil
}

// rowQuerier es lo que tienen en común *sql.DB y *sql.Tx.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// getServer devuelve un servidor con el dueño y la cantidad de miembros, o
// ErrServerNotFound si no hay fila. No mira si está borrado ni si tiene foto:
// quien lo llama ya lo verificó (por ejemplo, tras bloquear la fila).
func getServer(ctx context.Context, q rowQuerier, serverID string) (*model.Server, error) {
	s, err := scanServer(q.QueryRowContext(ctx, `
		SELECT `+serverColumns+`
		  FROM servers s LEFT JOIN users u ON u.user_id = s.owner_id
		 WHERE s.server_id = $1`, serverID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrServerNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("leer el servidor %s: %w", serverID, err)
	}
	return &s, nil
}

func (r *postgresServerRepository) List(ctx context.Context, filter ServerFilter, limit, offset int) ([]model.Server, int, error) {
	pattern := likePattern(filter.Search)
	status := string(filter.Status)

	var total int
	if err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM servers WHERE`+serverFilterClause, filter.Search, pattern, status,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("contar servidores: %w", err)
	}
	if total == 0 || offset >= total {
		return []model.Server{}, total, nil
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT `+serverColumns+`
		  FROM (SELECT * FROM servers
		         WHERE`+serverFilterClause+`
		         ORDER BY created_at DESC, server_id
		         LIMIT $4 OFFSET $5) s
		  LEFT JOIN users u ON u.user_id = s.owner_id
		 ORDER BY s.created_at DESC, s.server_id`,
		filter.Search, pattern, status, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listar servidores: %w", err)
	}
	defer rows.Close()

	servers := []model.Server{}
	for rows.Next() {
		s, err := scanServer(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("leer un servidor: %w", err)
		}
		servers = append(servers, s)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return servers, total, nil
}
