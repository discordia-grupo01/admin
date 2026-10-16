package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/discordia-grupo01/admin/internal/model"
)

// ServerFilter son los filtros del listado. Los campos vacíos no filtran.
type ServerFilter struct {
	// Search busca por nombre, parcial y sin distinguir mayúsculas.
	Search string
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
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("borrar el servidor %s: %w", serverID, err)
	}
	return nil
}

func (r *postgresServerRepository) SetMembership(ctx context.Context, m Membership) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO server_members (server_id, user_id, is_member, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (server_id, user_id) DO UPDATE SET
			is_member  = EXCLUDED.is_member,
			updated_at = EXCLUDED.updated_at
		WHERE server_members.updated_at < EXCLUDED.updated_at`,
		m.ServerID, m.UserID, m.IsMember, m.OccurredAt)
	if err != nil {
		return fmt.Errorf("guardar la membresía de %s en %s: %w", m.UserID, m.ServerID, err)
	}
	return nil
}

const serverFilterClause = `
	created_at IS NOT NULL
	AND deleted_at IS NULL
	AND ($1 = '' OR name ILIKE $2 ESCAPE '\')`

func (r *postgresServerRepository) List(ctx context.Context, filter ServerFilter, limit, offset int) ([]model.Server, int, error) {
	pattern := likePattern(filter.Search)

	var total int
	if err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM servers WHERE`+serverFilterClause, filter.Search, pattern,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("contar servidores: %w", err)
	}
	if total == 0 || offset >= total {
		return []model.Server{}, total, nil
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT s.server_id, s.name, s.owner_id, u.name, s.created_at,
		       (SELECT count(*) FROM server_members m WHERE m.server_id = s.server_id AND m.is_member)
		  FROM (SELECT * FROM servers
		         WHERE`+serverFilterClause+`
		         ORDER BY created_at DESC, server_id
		         LIMIT $3 OFFSET $4) s
		  LEFT JOIN users u ON u.user_id = s.owner_id
		 ORDER BY s.created_at DESC, s.server_id`,
		filter.Search, pattern, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listar servidores: %w", err)
	}
	defer rows.Close()

	servers := []model.Server{}
	for rows.Next() {
		var s model.Server
		var name, ownerID, ownerName sql.NullString
		if err := rows.Scan(&s.ID, &name, &ownerID, &ownerName, &s.CreatedAt, &s.MemberCount); err != nil {
			return nil, 0, fmt.Errorf("leer un servidor: %w", err)
		}
		s.Name, s.OwnerID = name.String, ownerID.String
		if ownerName.Valid {
			s.OwnerName = &ownerName.String
		}
		s.CreatedAt = s.CreatedAt.UTC()
		servers = append(servers, s)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return servers, total, nil
}
