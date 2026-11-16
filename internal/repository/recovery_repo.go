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
	ErrRecoveryRequestNotFound = errors.New("solicitud de recuperación no encontrada")
	ErrRecoveryRequestResolved = errors.New("la solicitud de recuperación ya fue resuelta")
)

// RecoveryRequestRecord es la solicitud tal como llega en users.account_recovery_requested.
type RecoveryRequestRecord struct {
	RequestID   string
	UserID      string
	Message     string
	RequestedAt time.Time
}

// RecoveryRequestFilter son los filtros del listado. Los campos vacíos no filtran.
type RecoveryRequestFilter struct {
	// Search busca por email o nombre del usuario, parcial y sin distinguir mayúsculas.
	Search string
	Status model.RecoveryRequestStatus
}

// RecoveryEventBuilder arma el evento firmado que avisa la decisión a identify.
// Se llama dentro de la transacción, con el dueño de la solicitud.
type RecoveryEventBuilder func(userID string) (routingKey string, payload any)

type RecoveryRequestRepository interface {
	// Insert guarda la solicitud. Es idempotente: un evento repetido no cambia nada.
	Insert(ctx context.Context, r RecoveryRequestRecord) error
	// List devuelve una página de solicitudes que cumplen el filtro, de la más
	// reciente a la más vieja, y cuántas hay en total.
	List(ctx context.Context, filter RecoveryRequestFilter, limit, offset int) ([]model.RecoveryRequest, int, error)
	// Get devuelve la solicitud con sus solicitudes anteriores, o
	// ErrRecoveryRequestNotFound.
	Get(ctx context.Context, requestID string) (*model.RecoveryRequestDetail, error)
	// Resolve aprueba o rechaza la solicitud y escribe el evento en el outbox en
	// la misma transacción. Bloquea la fila, así que si dos miembros del staff
	// la resuelven a la vez solo uno gana; el otro recibe
	// ErrRecoveryRequestResolved. Devuelve ErrRecoveryRequestNotFound si no existe.
	Resolve(ctx context.Context, requestID, staffID string, status model.RecoveryRequestStatus, at time.Time, event RecoveryEventBuilder) (*model.RecoveryRequestDetail, error)
	// UserRequests devuelve una página de las solicitudes de un usuario, de la
	// más reciente a la más vieja, para el expediente.
	UserRequests(ctx context.Context, userID string, limit, offset int) ([]model.RecoveryRequest, int, error)
}

type postgresRecoveryRequestRepository struct {
	db *sql.DB
}

func NewRecoveryRequestRepository(db *sql.DB) RecoveryRequestRepository {
	return &postgresRecoveryRequestRepository{db: db}
}

func (r *postgresRecoveryRequestRepository) Insert(ctx context.Context, rec RecoveryRequestRecord) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO account_recovery_requests (request_id, user_id, message, requested_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (request_id) DO NOTHING`,
		rec.RequestID, rec.UserID, rec.Message, rec.RequestedAt)
	if err != nil {
		return fmt.Errorf("guardar la solicitud de recuperación %s: %w", rec.RequestID, err)
	}
	return nil
}

// recoveryUserStatusExpr es userStatusExpr con las columnas de la réplica de
// usuarios calificadas: la solicitud también tiene una columna status.
const recoveryUserStatusExpr = `
	CASE WHEN u.status = 'suspended' THEN 'suspended'
	     WHEN NOT u.email_verified THEN 'pending'
	     ELSE 'active' END`

const recoverySelect = `
	SELECT r.request_id, r.user_id, r.message, r.status, r.requested_at, r.resolved_at, r.resolved_by,
	       s.name,
	       u.registered_at IS NOT NULL, u.name, u.email, u.avatar_url,` + recoveryUserStatusExpr + `, u.registered_at, u.suspended_at
	  FROM account_recovery_requests r
	  LEFT JOIN users u ON u.user_id = r.user_id
	  LEFT JOIN users s ON s.user_id = r.resolved_by`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRecoveryRequest(row rowScanner) (model.RecoveryRequest, error) {
	var (
		req                 model.RecoveryRequest
		status              string
		resolvedBy          sql.NullString
		staffName           sql.NullString
		registered          sql.NullBool
		name, email, uState sql.NullString
		avatarURL           sql.NullString
		registeredAt        sql.NullTime
		suspendedAt         sql.NullTime
	)
	if err := row.Scan(&req.ID, &req.UserID, &req.Message, &status, &req.RequestedAt, &req.ResolvedAt, &resolvedBy,
		&staffName, &registered, &name, &email, &avatarURL, &uState, &registeredAt, &suspendedAt); err != nil {
		return req, err
	}
	req.Status = model.RecoveryRequestStatus(status)
	req.RequestedAt = req.RequestedAt.UTC()
	if req.ResolvedAt != nil {
		t := req.ResolvedAt.UTC()
		req.ResolvedAt = &t
	}
	if resolvedBy.Valid {
		req.ResolvedBy = &model.StaffRef{ID: resolvedBy.String, Name: nullable(staffName)}
	}
	if registered.Bool {
		u := &model.User{
			ID:        req.UserID,
			Name:      name.String,
			Email:     email.String,
			AvatarURL: optionalText(avatarURL),
			Status:    model.UserStatus(uState.String),
			CreatedAt: registeredAt.Time.UTC(),
		}
		if suspendedAt.Valid {
			t := suspendedAt.Time.UTC()
			u.SuspendedAt = &t
		}
		req.User = u
	}
	return req, nil
}

func (r *postgresRecoveryRequestRepository) query(ctx context.Context, what, query string, args ...any) ([]model.RecoveryRequest, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listar %s: %w", what, err)
	}
	defer rows.Close()
	out := []model.RecoveryRequest{}
	for rows.Next() {
		req, err := scanRecoveryRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("leer una solicitud de recuperación: %w", err)
		}
		out = append(out, req)
	}
	return out, rows.Err()
}

const recoveryFilterClause = `
	($1 = '' OR u.email ILIKE $2 ESCAPE '\' OR u.name ILIKE $2 ESCAPE '\')
	AND ($3 = '' OR r.status = $3)`

func (r *postgresRecoveryRequestRepository) List(ctx context.Context, filter RecoveryRequestFilter, limit, offset int) ([]model.RecoveryRequest, int, error) {
	pattern := likePattern(filter.Search)
	status := string(filter.Status)

	var total int
	if err := r.db.QueryRowContext(ctx, `
		SELECT count(*)
		  FROM account_recovery_requests r
		  LEFT JOIN users u ON u.user_id = r.user_id
		 WHERE`+recoveryFilterClause, filter.Search, pattern, status,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("contar solicitudes de recuperación: %w", err)
	}
	if total == 0 || offset >= total {
		return []model.RecoveryRequest{}, total, nil
	}

	requests, err := r.query(ctx, "solicitudes de recuperación", recoverySelect+`
		 WHERE`+recoveryFilterClause+`
		 ORDER BY r.requested_at DESC, r.request_id
		 LIMIT $4 OFFSET $5`,
		filter.Search, pattern, status, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	return requests, total, nil
}

func (r *postgresRecoveryRequestRepository) Get(ctx context.Context, requestID string) (*model.RecoveryRequestDetail, error) {
	return getRecoveryRequest(ctx, r.db, requestID)
}

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func getRecoveryRequest(ctx context.Context, db queryRower, requestID string) (*model.RecoveryRequestDetail, error) {
	req, err := scanRecoveryRequest(db.QueryRowContext(ctx, recoverySelect+` WHERE r.request_id = $1`, requestID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRecoveryRequestNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("leer la solicitud de recuperación %s: %w", requestID, err)
	}

	detail := &model.RecoveryRequestDetail{RecoveryRequest: req}
	if err := db.QueryRowContext(ctx, `
		SELECT count(*), count(*) FILTER (WHERE status = 'rejected')
		  FROM account_recovery_requests
		 WHERE user_id = $1 AND (requested_at, request_id) < ($2, $3)`,
		req.UserID, req.RequestedAt, req.ID,
	).Scan(&detail.Previous.Total, &detail.Previous.Rejected); err != nil {
		return nil, fmt.Errorf("contar las solicitudes anteriores de %s: %w", req.UserID, err)
	}
	return detail, nil
}

func (r *postgresRecoveryRequestRepository) Resolve(ctx context.Context, requestID, staffID string, status model.RecoveryRequestStatus, at time.Time, event RecoveryEventBuilder) (*model.RecoveryRequestDetail, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("resolver la solicitud %s: %w", requestID, err)
	}
	defer func() { _ = tx.Rollback() }()

	var userID, current string
	err = tx.QueryRowContext(ctx, `
		SELECT user_id, status FROM account_recovery_requests WHERE request_id = $1 FOR UPDATE`,
		requestID).Scan(&userID, &current)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRecoveryRequestNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("bloquear la solicitud %s: %w", requestID, err)
	}
	if model.RecoveryRequestStatus(current) != model.RecoveryRequestPending {
		return nil, ErrRecoveryRequestResolved
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE account_recovery_requests
		   SET status = $2, resolved_at = $3, resolved_by = $4
		 WHERE request_id = $1`,
		requestID, string(status), at.UTC(), staffID); err != nil {
		return nil, fmt.Errorf("resolver la solicitud %s: %w", requestID, err)
	}
	key, payload := event(userID)
	if err := outbox.Insert(ctx, tx, key, payload); err != nil {
		return nil, err
	}
	detail, err := getRecoveryRequest(ctx, tx, requestID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("resolver la solicitud %s: %w", requestID, err)
	}
	return detail, nil
}

func (r *postgresRecoveryRequestRepository) UserRequests(ctx context.Context, userID string, limit, offset int) ([]model.RecoveryRequest, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM account_recovery_requests WHERE user_id = $1`, userID,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("contar las solicitudes de recuperación de %s: %w", userID, err)
	}
	if total == 0 || offset >= total {
		return []model.RecoveryRequest{}, total, nil
	}
	requests, err := r.query(ctx, "las solicitudes de recuperación del usuario", recoverySelect+`
		 WHERE r.user_id = $1
		 ORDER BY r.requested_at DESC, r.request_id
		 LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	return requests, total, nil
}
