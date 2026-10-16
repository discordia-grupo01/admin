package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/repository"
)

// ErrInvalidStatus indica un filtro de estado que no es active ni suspended.
var ErrInvalidStatus = errors.New("estado inválido: debe ser active o suspended")

type ListUsersParams struct {
	Search string
	// Status vacío = todos.
	Status model.UserStatus
	Limit  int
	Offset int
}

type UserService struct {
	users repository.UserRepository
}

func NewUserService(users repository.UserRepository) *UserService {
	return &UserService{users: users}
}

// FindIDByEmail devuelve el id del usuario con ese email, o
// repository.ErrUserNotFound.
func (s *UserService) FindIDByEmail(ctx context.Context, email string) (string, error) {
	return s.users.FindIDByEmail(ctx, email)
}

// ApplyRegistration da de alta al usuario en la réplica (users.account_registered).
func (s *UserService) ApplyRegistration(ctx context.Context, r repository.Registration) error {
	return s.users.UpsertRegistration(ctx, r)
}

// ApplyProfile actualiza el nombre del usuario (users.profile_updated); se
// ignora si la versión no es mayor a la guardada.
func (s *UserService) ApplyProfile(ctx context.Context, p repository.Profile) error {
	return s.users.UpsertProfile(ctx, p)
}

// List devuelve una página de usuarios con los filtros pedidos. El límite se
// normaliza (default 20, máximo 100).
func (s *UserService) List(ctx context.Context, p ListUsersParams) (*model.UserList, error) {
	if p.Status != "" && !p.Status.Valid() {
		return nil, ErrInvalidStatus
	}
	limit := model.NormalizeLimit(p.Limit)

	users, total, err := s.users.List(ctx, repository.UserFilter{Search: p.Search, Status: p.Status}, limit, p.Offset)
	if err != nil {
		return nil, fmt.Errorf("listar usuarios: %w", err)
	}
	return &model.UserList{Users: users, Total: total, Limit: limit, Offset: p.Offset}, nil
}
