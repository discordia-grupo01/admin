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
