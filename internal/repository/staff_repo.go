package repository

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type StaffRepository interface {
	IsStaff(ctx context.Context, userID string) (bool, error)
	// AddAll da de alta a los usuarios como staff; los que ya lo eran se ignoran.
	AddAll(ctx context.Context, userIDs []string) error
}

type StaffEventSeeder interface {
	AddAllWithEvents(ctx context.Context, userIDs []string, secret string) error
}

type postgresStaffRepository struct {
	db *sql.DB
}

func NewStaffRepository(db *sql.DB) StaffRepository {
	return &postgresStaffRepository{db: db}
}

func (r *postgresStaffRepository) IsStaff(ctx context.Context, userID string) (bool, error) {
	var ok bool
	err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM staff_members WHERE user_id = $1)`, userID).Scan(&ok)
	return ok, err
}

func (r *postgresStaffRepository) AddAll(ctx context.Context, userIDs []string) error {
	return r.addAll(ctx, userIDs, "")
}

func (r *postgresStaffRepository) AddAllWithEvents(ctx context.Context, userIDs []string, secret string) error {
	if strings.TrimSpace(secret) == "" {
		return fmt.Errorf("ADMIN_EVENTS_HMAC_SECRET es obligatorio para publicar cambios de staff")
	}
	return r.addAll(ctx, userIDs, secret)
}

func (r *postgresStaffRepository) addAll(ctx context.Context, userIDs []string, secret string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, id := range userIDs {
		result, err := tx.ExecContext(ctx,
			`INSERT INTO staff_members (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, id)
		if err != nil {
			return fmt.Errorf("alta de staff %s: %w", id, err)
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("comprobar alta de staff %s: %w", id, err)
		}
		if secret != "" && inserted == 1 {
			eventID := uuid.NewString()
			occurredAt := time.Now().UTC()
			signature := signStaffEvent(secret, staffAddedRoutingKey, eventID, id, occurredAt)
			payload := fmt.Sprintf(`{"event_id":%q,"occurred_at":%q,"user_id":%q,"signature":%q}`,
				eventID, occurredAt.Format(time.RFC3339Nano), id, signature)
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO outbox_events (id, routing_key, payload)
				VALUES ($1, $2, $3)`, eventID, staffAddedRoutingKey, []byte(payload)); err != nil {
				return fmt.Errorf("evento de alta de staff %s: %w", id, err)
			}
		}
	}
	return tx.Commit()
}

const staffAddedRoutingKey = "admin.staff_member_added"

func signStaffEvent(secret, routingKey, eventID, userID string, occurredAt time.Time) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strings.Join([]string{
		routingKey, eventID, userID, occurredAt.UTC().Format(time.RFC3339Nano),
	}, "\n")))
	return hex.EncodeToString(mac.Sum(nil))
}
