package service

import (
	"context"
	"fmt"
	"time"

	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/repository"
)

type ListServersParams struct {
	Search string
	Limit  int
	Offset int
}

type ServerService struct {
	servers repository.ServerRepository
}

func NewServerService(servers repository.ServerRepository) *ServerService {
	return &ServerService{servers: servers}
}

// ApplyServerUpdated guarda la foto del servidor (servers.server_updated); se
// ignora si no es más nueva que la guardada.
func (s *ServerService) ApplyServerUpdated(ctx context.Context, snap repository.ServerSnapshot) error {
	return s.servers.UpsertServer(ctx, snap)
}

// ApplyServerDeleted oculta el servidor del listado (servers.server_deleted).
func (s *ServerService) ApplyServerDeleted(ctx context.Context, serverID string, at time.Time) error {
	return s.servers.MarkDeleted(ctx, serverID, at)
}

// ApplyMembership registra que un usuario entró o salió de un servidor
// (servers.member_joined / servers.member_left).
func (s *ServerService) ApplyMembership(ctx context.Context, m repository.Membership) error {
	return s.servers.SetMembership(ctx, m)
}

// List devuelve una página de servidores con el filtro pedido. El límite se
// normaliza (default 20, máximo 100).
func (s *ServerService) List(ctx context.Context, p ListServersParams) (*model.ServerList, error) {
	limit := model.NormalizeLimit(p.Limit)

	servers, total, err := s.servers.List(ctx, repository.ServerFilter{Search: p.Search}, limit, p.Offset)
	if err != nil {
		return nil, fmt.Errorf("listar servidores: %w", err)
	}
	return &model.ServerList{Servers: servers, Total: total, Limit: limit, Offset: p.Offset}, nil
}
