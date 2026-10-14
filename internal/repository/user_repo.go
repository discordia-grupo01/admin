package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/discordia-grupo01/admin/internal/model"
)

// UserFilter son los filtros del listado. Los campos vacíos no filtran.
type UserFilter struct {
	// Search busca por email o nombre, parcial y sin distinguir mayúsculas.
	Search string
	Status model.UserStatus
}

type UserRepository interface {
	// List devuelve una página de usuarios que cumplen el filtro, del más
	// reciente al más viejo, y cuántos hay en total. Solo incluye usuarios cuyo
	// registro ya llegó (registered_at no nulo).
	List(ctx context.Context, filter UserFilter, limit, offset int) ([]model.User, int, error)
}

type postgresUserRepository struct {
	db *sql.DB
}

func NewUserRepository(db *sql.DB) UserRepository {
	return &postgresUserRepository{db: db}
}

// likeEscaper escapa los comodines de LIKE para que la búsqueda trate % y _
// como texto literal (con ESCAPE '\' en la consulta).
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func likePattern(search string) string {
	return "%" + likeEscaper.Replace(search) + "%"
}

const userFilterClause = `
	registered_at IS NOT NULL
	AND ($1 = '' OR email ILIKE $2 ESCAPE '\' OR name ILIKE $2 ESCAPE '\')
	AND ($3 = '' OR status = $3)`

func (r *postgresUserRepository) List(ctx context.Context, filter UserFilter, limit, offset int) ([]model.User, int, error) {
	pattern := likePattern(filter.Search)
	status := string(filter.Status)

	var total int
	if err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM users WHERE`+userFilterClause, filter.Search, pattern, status,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("contar usuarios: %w", err)
	}
	if total == 0 || offset >= total {
		return []model.User{}, total, nil
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT user_id, name, email, status, registered_at, suspended_at
		  FROM users
		 WHERE`+userFilterClause+`
		 ORDER BY registered_at DESC, user_id
		 LIMIT $4 OFFSET $5`,
		filter.Search, pattern, status, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listar usuarios: %w", err)
	}
	defer rows.Close()

	users := []model.User{}
	for rows.Next() {
		var u model.User
		var name, email sql.NullString
		if err := rows.Scan(&u.ID, &name, &email, &u.Status, &u.CreatedAt, &u.SuspendedAt); err != nil {
			return nil, 0, fmt.Errorf("leer un usuario: %w", err)
		}
		u.Name, u.Email = name.String, email.String
		u.CreatedAt = u.CreatedAt.UTC()
		if u.SuspendedAt != nil {
			t := u.SuspendedAt.UTC()
			u.SuspendedAt = &t
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return users, total, nil
}
