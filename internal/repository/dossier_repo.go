package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/discordia-grupo01/admin/internal/model"
)

// ActivityEntry es una fila de actividad con lo necesario para describirla.
type ActivityEntry struct {
	ID          string
	Kind        string
	ServerID    string
	ServerName  string // vacío si el servidor es desconocido
	ChannelName string // vacío si no vino en el evento
	OccurredAt  time.Time
}

// DossierRepository lee el expediente. Cada método es una consulta
// independiente: el expediente es una vista de réplicas con consistencia
// eventual, no hace falta una foto transaccional.
type DossierRepository interface {
	// FindUser devuelve el usuario como lo ve el listado, o ErrUserNotFound si
	// no existe o todavía no llegó su registro.
	FindUser(ctx context.Context, userID string) (*model.User, error)
	// FindServer devuelve el servidor como lo ve el listado, o ErrServerNotFound
	// si no existe, no tiene foto todavía o fue borrado.
	FindServer(ctx context.Context, serverID string) (*model.Server, error)

	UserMemberships(ctx context.Context, userID string, limit, offset int) ([]model.DossierMembership, int, error)
	UserActivity(ctx context.Context, userID string, limit, offset int) ([]ActivityEntry, int, error)
	// UserLastActivity es null si el usuario no tiene actividad registrada.
	UserLastActivity(ctx context.Context, userID string) (*time.Time, error)
	UserReports(ctx context.Context, userID string, limit, offset int) ([]model.DossierReport, int, error)
	UserModeration(ctx context.Context, userID string, limit, offset int) ([]model.DossierModerationAction, int, error)

	ServerReports(ctx context.Context, serverID string, limit, offset int) ([]model.DossierReport, int, error)
	ServerModeration(ctx context.Context, serverID string, limit, offset int) ([]model.DossierModerationAction, int, error)
	// ServerMessaging devuelve los mensajes por día UTC de los 7 días que
	// terminan en today (incluido), del más viejo al más nuevo y con ceros.
	ServerMessaging(ctx context.Context, serverID string, today time.Time) ([]model.MessagingDay, error)
}

type postgresDossierRepository struct {
	db *sql.DB
}

func NewDossierRepository(db *sql.DB) DossierRepository {
	return &postgresDossierRepository{db: db}
}

func (r *postgresDossierRepository) FindUser(ctx context.Context, userID string) (*model.User, error) {
	var u model.User
	var name, email, avatarURL sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT user_id, name, email, avatar_url,`+userStatusExpr+`, registered_at, suspended_at
		  FROM users
		 WHERE user_id = $1 AND registered_at IS NOT NULL`, userID,
	).Scan(&u.ID, &name, &email, &avatarURL, &u.Status, &u.CreatedAt, &u.SuspendedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("leer el usuario %s: %w", userID, err)
	}
	u.Name, u.Email, u.AvatarURL = name.String, email.String, optionalText(avatarURL)
	u.CreatedAt = u.CreatedAt.UTC()
	if u.SuspendedAt != nil {
		t := u.SuspendedAt.UTC()
		u.SuspendedAt = &t
	}
	return &u, nil
}

func (r *postgresDossierRepository) FindServer(ctx context.Context, serverID string) (*model.Server, error) {
	s, err := scanServer(r.db.QueryRowContext(ctx, `
		SELECT `+serverColumns+`
		  FROM servers s LEFT JOIN users u ON u.user_id = s.owner_id
		 WHERE s.server_id = $1 AND s.created_at IS NOT NULL AND s.deleted_at IS NULL`, serverID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrServerNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("leer el servidor %s: %w", serverID, err)
	}
	return &s, nil
}

func (r *postgresDossierRepository) count(ctx context.Context, what, query string, args ...any) (int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("contar %s: %w", what, err)
	}
	return total, nil
}

const userMembershipsWhere = `
	  FROM server_members m JOIN servers s ON s.server_id = m.server_id
	 WHERE m.user_id = $1 AND m.is_member AND s.deleted_at IS NULL`

func (r *postgresDossierRepository) UserMemberships(ctx context.Context, userID string, limit, offset int) ([]model.DossierMembership, int, error) {
	total, err := r.count(ctx, "los servidores del usuario", `SELECT count(*)`+userMembershipsWhere, userID)
	if err != nil || total == 0 || offset >= total {
		return []model.DossierMembership{}, total, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.server_id, s.name, s.icon_url, m.updated_at`+userMembershipsWhere+`
		 ORDER BY m.updated_at DESC, s.server_id
		 LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listar los servidores del usuario %s: %w", userID, err)
	}
	defer rows.Close()
	out := []model.DossierMembership{}
	for rows.Next() {
		var m model.DossierMembership
		var name, iconURL sql.NullString
		if err := rows.Scan(&m.ServerID, &name, &iconURL, &m.JoinedAt); err != nil {
			return nil, 0, fmt.Errorf("leer un servidor del usuario: %w", err)
		}
		m.ServerName = nullable(name)
		m.ServerIconURL = optionalText(iconURL)
		m.JoinedAt = m.JoinedAt.UTC()
		out = append(out, m)
	}
	return out, total, rows.Err()
}

func (r *postgresDossierRepository) UserActivity(ctx context.Context, userID string, limit, offset int) ([]ActivityEntry, int, error) {
	total, err := r.count(ctx, "la actividad del usuario", `SELECT count(*) FROM user_activity WHERE user_id = $1`, userID)
	if err != nil || total == 0 || offset >= total {
		return []ActivityEntry{}, total, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT a.id, a.kind, a.server_id, s.name, a.channel_name, a.occurred_at
		  FROM user_activity a LEFT JOIN servers s ON s.server_id = a.server_id
		 WHERE a.user_id = $1
		 ORDER BY a.occurred_at DESC, a.id
		 LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listar la actividad del usuario %s: %w", userID, err)
	}
	defer rows.Close()
	out := []ActivityEntry{}
	for rows.Next() {
		var e ActivityEntry
		var serverName, channelName sql.NullString
		if err := rows.Scan(&e.ID, &e.Kind, &e.ServerID, &serverName, &channelName, &e.OccurredAt); err != nil {
			return nil, 0, fmt.Errorf("leer una actividad: %w", err)
		}
		e.ServerName, e.ChannelName = serverName.String, channelName.String
		e.OccurredAt = e.OccurredAt.UTC()
		out = append(out, e)
	}
	return out, total, rows.Err()
}

func (r *postgresDossierRepository) UserLastActivity(ctx context.Context, userID string) (*time.Time, error) {
	var at sql.NullTime
	if err := r.db.QueryRowContext(ctx,
		`SELECT max(occurred_at) FROM user_activity WHERE user_id = $1`, userID).Scan(&at); err != nil {
		return nil, fmt.Errorf("leer la última actividad de %s: %w", userID, err)
	}
	if !at.Valid {
		return nil, nil
	}
	t := at.Time.UTC()
	return &t, nil
}

// reportSelect arma un reporte con los nombres. El autor de un reporte de
// mensaje se resuelve acá, al leer, cruzando con la actividad por message_id:
// así no importa si message_created llegó antes o después que el reporte.
const reportSelect = `
	SELECT r.report_id, r.reason, r.created_at, r.kind, r.status, r.resolved_at,
	       r.server_id, s.name, who.user_id, u.name
	  FROM reports r
	  LEFT JOIN servers s ON s.server_id = r.server_id
	  LEFT JOIN LATERAL (
	        SELECT COALESCE(r.reported_user_id,
	                        (SELECT a.user_id FROM user_activity a
	                          WHERE r.message_id IS NOT NULL AND a.message_id = r.message_id
	                          LIMIT 1)) AS user_id) who ON TRUE
	  LEFT JOIN users u ON u.user_id = who.user_id`

// Los reportes de un usuario: los suyos como persona reportada y los de
// mensajes que él escribió.
const userReportsWhere = `
	WHERE r.reported_user_id = $1
	   OR r.message_id IN (SELECT message_id FROM user_activity WHERE user_id = $1 AND message_id IS NOT NULL)`

func (r *postgresDossierRepository) UserReports(ctx context.Context, userID string, limit, offset int) ([]model.DossierReport, int, error) {
	total, err := r.count(ctx, "los reportes del usuario", `SELECT count(*) FROM reports r`+userReportsWhere, userID)
	if err != nil || total == 0 || offset >= total {
		return []model.DossierReport{}, total, err
	}
	return r.reports(ctx, reportSelect+userReportsWhere+`
		 ORDER BY r.created_at DESC, r.report_id LIMIT $2 OFFSET $3`, total, userID, limit, offset)
}

func (r *postgresDossierRepository) ServerReports(ctx context.Context, serverID string, limit, offset int) ([]model.DossierReport, int, error) {
	total, err := r.count(ctx, "los reportes del servidor", `SELECT count(*) FROM reports WHERE server_id = $1`, serverID)
	if err != nil || total == 0 || offset >= total {
		return []model.DossierReport{}, total, err
	}
	return r.reports(ctx, reportSelect+`
		 WHERE r.server_id = $1
		 ORDER BY r.created_at DESC, r.report_id LIMIT $2 OFFSET $3`, total, serverID, limit, offset)
}

func (r *postgresDossierRepository) reports(ctx context.Context, query string, total int, args ...any) ([]model.DossierReport, int, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("listar reportes: %w", err)
	}
	defer rows.Close()
	out := []model.DossierReport{}
	for rows.Next() {
		var rep model.DossierReport
		var resolved sql.NullTime
		var serverName, reportedID, reportedName sql.NullString
		if err := rows.Scan(&rep.ID, &rep.Reason, &rep.CreatedAt, &rep.Kind, &rep.Status, &resolved,
			&rep.ServerID, &serverName, &reportedID, &reportedName); err != nil {
			return nil, 0, fmt.Errorf("leer un reporte: %w", err)
		}
		rep.CreatedAt = rep.CreatedAt.UTC()
		if resolved.Valid {
			t := resolved.Time.UTC()
			rep.ResolvedAt = &t
		}
		rep.ServerName, rep.ReportedUserID, rep.ReportedUserName = nullable(serverName), nullable(reportedID), nullable(reportedName)
		out = append(out, rep)
	}
	return out, total, rows.Err()
}

const moderationSelect = `
	SELECT m.id, m.kind, m.occurred_at, m.scope, m.server_id, s.name, m.target_user_id, u.name
	  FROM moderation_actions m
	  LEFT JOIN servers s ON s.server_id = m.server_id
	  LEFT JOIN users u ON u.user_id = m.target_user_id`

func (r *postgresDossierRepository) UserModeration(ctx context.Context, userID string, limit, offset int) ([]model.DossierModerationAction, int, error) {
	total, err := r.count(ctx, "la moderación del usuario", `SELECT count(*) FROM moderation_actions WHERE target_user_id = $1`, userID)
	if err != nil || total == 0 || offset >= total {
		return []model.DossierModerationAction{}, total, err
	}
	return r.moderation(ctx, moderationSelect+`
		 WHERE m.target_user_id = $1
		 ORDER BY m.occurred_at DESC, m.id LIMIT $2 OFFSET $3`, total, userID, limit, offset)
}

func (r *postgresDossierRepository) ServerModeration(ctx context.Context, serverID string, limit, offset int) ([]model.DossierModerationAction, int, error) {
	total, err := r.count(ctx, "la moderación del servidor", `SELECT count(*) FROM moderation_actions WHERE server_id = $1`, serverID)
	if err != nil || total == 0 || offset >= total {
		return []model.DossierModerationAction{}, total, err
	}
	return r.moderation(ctx, moderationSelect+`
		 WHERE m.server_id = $1
		 ORDER BY m.occurred_at DESC, m.id LIMIT $2 OFFSET $3`, total, serverID, limit, offset)
}

func (r *postgresDossierRepository) moderation(ctx context.Context, query string, total int, args ...any) ([]model.DossierModerationAction, int, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("listar acciones de moderación: %w", err)
	}
	defer rows.Close()
	out := []model.DossierModerationAction{}
	for rows.Next() {
		var a model.DossierModerationAction
		var serverID, serverName, targetID, targetName sql.NullString
		if err := rows.Scan(&a.ID, &a.Kind, &a.AppliedAt, &a.Scope, &serverID, &serverName, &targetID, &targetName); err != nil {
			return nil, 0, fmt.Errorf("leer una acción de moderación: %w", err)
		}
		a.AppliedAt = a.AppliedAt.UTC()
		a.ServerID, a.ServerName, a.TargetUserID, a.TargetUserName = nullable(serverID), nullable(serverName), nullable(targetID), nullable(targetName)
		out = append(out, a)
	}
	return out, total, rows.Err()
}

func (r *postgresDossierRepository) ServerMessaging(ctx context.Context, serverID string, today time.Time) ([]model.MessagingDay, error) {
	// Los días son UTC: un mensaje de las 22 h en UTC-3 cae en el día siguiente.
	rows, err := r.db.QueryContext(ctx, `
		SELECT d::date::text, COALESCE(c.n, 0)
		  FROM generate_series($2::date - 6, $2::date, interval '1 day') d
		  LEFT JOIN (
		        SELECT (occurred_at AT TIME ZONE 'UTC')::date AS day, count(*) AS n
		          FROM user_activity
		         WHERE server_id = $1 AND kind = 'message_sent'
		           AND occurred_at >= (($2::date - 6)::timestamp AT TIME ZONE 'UTC')
		           AND occurred_at <  (($2::date + 1)::timestamp AT TIME ZONE 'UTC')
		         GROUP BY 1) c ON c.day = d::date
		 ORDER BY d`, serverID, today.UTC().Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("leer la mensajería del servidor %s: %w", serverID, err)
	}
	defer rows.Close()
	out := make([]model.MessagingDay, 0, 7)
	for rows.Next() {
		var d model.MessagingDay
		if err := rows.Scan(&d.Date, &d.Messages); err != nil {
			return nil, fmt.Errorf("leer un día de mensajería: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// nullable devuelve nil si el valor es NULL.
func nullable(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}
