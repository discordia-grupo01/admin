package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/outbox"
)

var (
	// ErrAlreadySuspended indica que el usuario ya estaba suspendido.
	ErrAlreadySuspended = errors.New("el usuario ya está suspendido")
	// ErrTargetIsStaff indica que no se puede suspender a un miembro del staff.
	ErrTargetIsStaff = errors.New("no se puede suspender a un miembro del staff")
)

// EventBuilder arma el evento a publicar una vez conocida la versión de
// suspensión que le toca al usuario. Se llama dentro de la transacción.
type EventBuilder func(version int64) (routingKey string, payload any)

type SuspensionRepository interface {
	// Suspend suspende al usuario y escribe el evento en el outbox, todo en una
	// misma transacción: o quedan las dos cosas o ninguna. Bloquea la fila del
	// usuario, así que dos suspensiones simultáneas no pueden pasar las dos.
	//
	// Devuelve ErrTargetIsStaff si el usuario es staff, ErrUserNotFound si no
	// está en la réplica (o todavía no llegó su registro) y ErrAlreadySuspended
	// si ya lo estaba. En esos casos no cambia nada ni se escribe ningún evento.
	Suspend(ctx context.Context, userID, staffID string, at time.Time, event EventBuilder) (*model.User, error)
}

type postgresSuspensionRepository struct {
	db *sql.DB
}

func NewSuspensionRepository(db *sql.DB) SuspensionRepository {
	return &postgresSuspensionRepository{db: db}
}

func (r *postgresSuspensionRepository) Suspend(ctx context.Context, userID, staffID string, at time.Time, event EventBuilder) (*model.User, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("suspender a %s: %w", userID, err)
	}
	defer func() { _ = tx.Rollback() }()

	// Primero el staff: aunque el staff no esté en la réplica, la respuesta es
	// la misma y no se le puede tocar el estado.
	var isStaff bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM staff_members WHERE user_id = $1)`, userID).Scan(&isStaff); err != nil {
		return nil, fmt.Errorf("verificar si %s es staff: %w", userID, err)
	}
	if isStaff {
		return nil, ErrTargetIsStaff
	}

	var status string
	var version int64
	err = tx.QueryRowContext(ctx, `
		SELECT status, suspension_version FROM users
		 WHERE user_id = $1 AND registered_at IS NOT NULL
		   FOR UPDATE`, userID).Scan(&status, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("leer a %s: %w", userID, err)
	}
	if model.UserStatus(status) == model.UserStatusSuspended {
		return nil, ErrAlreadySuspended
	}

	at = at.UTC()
	version++
	var u model.User
	var name, email sql.NullString
	if err := tx.QueryRowContext(ctx, `
		UPDATE users
		   SET status = 'suspended', suspended_at = $2, suspended_by = $3,
		       suspension_version = $4, updated_at = now()
		 WHERE user_id = $1
		RETURNING user_id, name, email, status, registered_at`,
		userID, at, staffID, version).Scan(&u.ID, &name, &email, &u.Status, &u.CreatedAt); err != nil {
		return nil, fmt.Errorf("suspender a %s: %w", userID, err)
	}
	u.Name, u.Email = name.String, email.String
	u.CreatedAt = u.CreatedAt.UTC()
	u.SuspendedAt = &at

	key, payload := event(version)
	if err := outbox.Insert(ctx, tx, key, payload); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("suspender a %s: %w", userID, err)
	}
	return &u, nil
}
