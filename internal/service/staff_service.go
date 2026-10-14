// Package service tiene la lógica de negocio de admin.
package service

import (
	"context"
	"fmt"
	"log"

	"github.com/discordia-grupo01/admin/internal/repository"
)

type StaffService struct {
	repo repository.StaffRepository
}

func NewStaffService(repo repository.StaffRepository) *StaffService {
	return &StaffService{repo: repo}
}

func (s *StaffService) IsStaff(ctx context.Context, userID string) (bool, error) {
	return s.repo.IsStaff(ctx, userID)
}

// Seed da de alta como staff a los usuarios de STAFF_USER_IDS. Es idempotente:
// se puede correr en cada arranque.
func (s *StaffService) Seed(ctx context.Context, userIDs []string) error {
	if len(userIDs) == 0 {
		log.Printf("staff: STAFF_USER_IDS vacío, no se siembra ningún usuario")
		return nil
	}
	if err := s.repo.AddAll(ctx, userIDs); err != nil {
		return fmt.Errorf("sembrar staff: %w", err)
	}
	log.Printf("staff: %d usuario(s) sembrados como staff", len(userIDs))
	return nil
}
