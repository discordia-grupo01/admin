package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/discordia-grupo01/admin/internal/model"
)

// UserFilter son los filtros del listado. Los campos vacíos no filtran.
type UserFilter struct {
	// Search busca por email o nombre, parcial y sin distinguir mayúsculas.
	Search string
	Status model.UserStatus
}

// ErrUserNotFound indica que no hay un usuario registrado con ese dato en la réplica.
var ErrUserNotFound = errors.New("usuario no encontrado")

// Registration son los datos de la cuenta que llegan en users.account_registered.
type Registration struct {
	UserID        string
	Email         string
	Name          string
	RegisteredAt  time.Time
	EmailVerified bool
}

// Profile es la parte del perfil público que admin guarda (users.profile_updated).
type Profile struct {
	UserID string
	Name   string
	// AvatarURL es la dirección de la foto tal como la publica identify, o
	// vacío si no tiene.
	AvatarURL string
	Version   int64
}

type UserRepository interface {
	// FindIDByEmail devuelve el id del usuario con ese email exacto (sin
	// distinguir mayúsculas), o ErrUserNotFound. Solo ve usuarios con registro.
	FindIDByEmail(ctx context.Context, email string) (string, error)
	// UpsertRegistration da de alta al usuario (o completa la fila que creó un
	// users.profile_updated que llegó antes). Es idempotente: repetirlo deja
	// los mismos valores, y nunca toca el estado de suspensión ni pisa un
	// nombre más nuevo que el del registro, ni des-verifica un email que ya
	// estaba verificado.
	UpsertRegistration(ctx context.Context, r Registration) error
	// MarkEmailVerified marca el email del usuario como verificado
	// (users.email_verified). Si el usuario todavía no está, crea la fila sin
	// registro: no aparece en el listado hasta que llegue el alta.
	MarkEmailVerified(ctx context.Context, userID string) error
	// UpsertProfile aplica la foto del perfil solo si su versión es mayor a la
	// guardada, así un evento repetido o fuera de orden no pisa datos nuevos.
	// Con la misma versión se aplica solo si todavía no se conoce el avatar
	// (lo completa el backfill de identify).
	UpsertProfile(ctx context.Context, p Profile) error

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

func (r *postgresUserRepository) FindIDByEmail(ctx context.Context, email string) (string, error) {
	var id string
	err := r.db.QueryRowContext(ctx, `
		SELECT user_id FROM users
		 WHERE registered_at IS NOT NULL AND lower(email) = lower($1)
		 ORDER BY registered_at
		 LIMIT 1`, email).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrUserNotFound
	}
	if err != nil {
		return "", fmt.Errorf("buscar el usuario por email: %w", err)
	}
	return id, nil
}

func (r *postgresUserRepository) UpsertRegistration(ctx context.Context, reg Registration) error {
	// profile_version = 0 significa que el nombre vino del registro: un
	// users.profile_updated posterior (version >= 1) siempre lo reemplaza, y si
	// ya había llegado, su nombre se conserva. La verificación del email nunca
	// vuelve atrás: un alta con email_verified = false (por ejemplo, la que
	// llega después de users.email_verified) no la pisa.
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO users (user_id, name, email, registered_at, email_verified)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id) DO UPDATE SET
			email          = EXCLUDED.email,
			registered_at  = EXCLUDED.registered_at,
			name           = CASE WHEN users.profile_version = 0 THEN EXCLUDED.name ELSE users.name END,
			email_verified = users.email_verified OR EXCLUDED.email_verified,
			updated_at     = now()`,
		reg.UserID, reg.Name, reg.Email, reg.RegisteredAt, reg.EmailVerified)
	if err != nil {
		return fmt.Errorf("guardar el registro de %s: %w", reg.UserID, err)
	}
	return nil
}

func (r *postgresUserRepository) MarkEmailVerified(ctx context.Context, userID string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO users (user_id, email_verified)
		VALUES ($1, TRUE)
		ON CONFLICT (user_id) DO UPDATE SET
			email_verified = TRUE,
			updated_at     = now()
		WHERE NOT users.email_verified`,
		userID)
	if err != nil {
		return fmt.Errorf("marcar el email de %s como verificado: %w", userID, err)
	}
	return nil
}

func (r *postgresUserRepository) UpsertProfile(ctx context.Context, p Profile) error {
	// El WHERE del DO UPDATE descarta las fotos con versión menor, y las de
	// versión igual una vez que el avatar ya se conoce (avatar_url no nulo).
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO users (user_id, name, avatar_url, profile_version)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id) DO UPDATE SET
			name            = EXCLUDED.name,
			avatar_url      = EXCLUDED.avatar_url,
			profile_version = EXCLUDED.profile_version,
			updated_at      = now()
		WHERE users.profile_version < EXCLUDED.profile_version
		   OR (users.profile_version = EXCLUDED.profile_version AND users.avatar_url IS NULL)`,
		p.UserID, p.Name, p.AvatarURL, p.Version)
	if err != nil {
		return fmt.Errorf("guardar el perfil de %s: %w", p.UserID, err)
	}
	return nil
}

// likeEscaper escapa los comodines de LIKE para que la búsqueda trate % y _
// como texto literal (con ESCAPE '\' en la consulta).
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func likePattern(search string) string {
	return "%" + likeEscaper.Replace(search) + "%"
}

// userStatusExpr es el estado que muestra el listado: la suspensión manda, y
// un usuario no suspendido que no confirmó su email está pendiente.
const userStatusExpr = `
	CASE WHEN status = 'suspended' THEN 'suspended'
	     WHEN NOT email_verified THEN 'pending'
	     ELSE 'active' END`

const userFilterClause = `
	registered_at IS NOT NULL
	AND ($1 = '' OR email ILIKE $2 ESCAPE '\' OR name ILIKE $2 ESCAPE '\')
	AND ($3 = '' OR` + userStatusExpr + ` = $3)`

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
		SELECT user_id, name, email, avatar_url,`+userStatusExpr+`, registered_at, suspended_at
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
		var name, email, avatarURL sql.NullString
		if err := rows.Scan(&u.ID, &name, &email, &avatarURL, &u.Status, &u.CreatedAt, &u.SuspendedAt); err != nil {
			return nil, 0, fmt.Errorf("leer un usuario: %w", err)
		}
		u.Name, u.Email, u.AvatarURL = name.String, email.String, optionalText(avatarURL)
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

// optionalText convierte una columna de texto en un puntero: nil si es NULL o
// vacía.
func optionalText(v sql.NullString) *string {
	if !v.Valid || v.String == "" {
		return nil
	}
	return &v.String
}
