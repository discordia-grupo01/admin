// Package service tiene la lógica de negocio de admin.
package service

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/discordia-grupo01/admin/internal/repository"
)

type StaffService struct {
	repo repository.StaffRepository
}

func NewStaffService(repo repository.StaffRepository) *StaffService {
	return &StaffService{repo: repo}
}

// UserLookup busca el id de un usuario por su email (lo implementa UserService).
type UserLookup interface {
	FindIDByEmail(ctx context.Context, email string) (string, error)
}

// SeedByEmail da de alta como staff al usuario con ese email, buscándolo en la
// réplica de usuarios, y devuelve su id. Es la forma de sembrar staff cuando
// solo se conoce el email. Falla con repository.ErrUserNotFound si el usuario
// no está en la réplica (todavía no se registró o su alta no llegó).
func (s *StaffService) SeedByEmail(ctx context.Context, users UserLookup, email string) (string, error) {
	email = strings.TrimSpace(email)
	if !strings.Contains(email, "@") {
		return "", fmt.Errorf("%q no parece un email", email)
	}
	id, err := users.FindIDByEmail(ctx, email)
	if err != nil {
		return "", err
	}
	if err := s.Seed(ctx, []string{id}); err != nil {
		return "", err
	}
	return id, nil
}

func (s *StaffService) SeedByEmailWithEvents(ctx context.Context, users UserLookup, email, secret string) (string, error) {
	email = strings.TrimSpace(email)
	if !strings.Contains(email, "@") {
		return "", fmt.Errorf("%q no parece un email", email)
	}
	id, err := users.FindIDByEmail(ctx, email)
	if err != nil {
		return "", err
	}
	if err := s.SeedWithEvents(ctx, []string{id}, secret); err != nil {
		return "", err
	}
	return id, nil
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

// SeedWithEvents siembra staff y publica una foto firmada para que otros
// servicios puedan mantener una proyección local de autorización.
func (s *StaffService) SeedWithEvents(ctx context.Context, userIDs []string, secret string) error {
	if len(userIDs) == 0 {
		log.Printf("staff: STAFF_USER_IDS vacío, no se siembra ningún usuario")
		return nil
	}
	if repo, ok := s.repo.(repository.StaffEventSeeder); ok {
		if err := repo.AddAllWithEvents(ctx, userIDs, secret); err != nil {
			return fmt.Errorf("sembrar staff y publicar eventos: %w", err)
		}
		log.Printf("staff: %d usuario(s) sembrados y publicados", len(userIDs))
		return nil
	}
	return s.Seed(ctx, userIDs)
}
