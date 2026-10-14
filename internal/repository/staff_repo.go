package repository

import (
	"context"
	"database/sql"
	"fmt"
)

type StaffRepository interface {
	IsStaff(ctx context.Context, userID string) (bool, error)
	// AddAll da de alta a los usuarios como staff; los que ya lo eran se ignoran.
	AddAll(ctx context.Context, userIDs []string) error
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
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, id := range userIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO staff_members (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, id); err != nil {
			return fmt.Errorf("alta de staff %s: %w", id, err)
		}
	}
	return tx.Commit()
}
