package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/discordia-grupo01/admin/internal/audit"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/events-contract/membership"
	"github.com/discordia-grupo01/events-contract/servers"
)

// QueueServers es la cola durable de admin para la réplica de servidores.
const QueueServers = "admin.servers"

// ServerBindings son las routing keys que alimentan la réplica de servidores.
var ServerBindings = []string{
	servers.RoutingKeyServerUpdated,
	servers.RoutingKeyServerDeleted,
	membership.RoutingKeyMemberJoined,
	membership.RoutingKeyMemberLeft,
}

// ServerSync es lo que el handler necesita para aplicar los eventos de servidores.
type ServerSync interface {
	ApplyServerUpdated(ctx context.Context, s repository.ServerSnapshot) error
	ApplyServerDeleted(ctx context.Context, serverID string, at time.Time) error
	ApplyMembership(ctx context.Context, m repository.Membership) error
}

// ServerEventsHandler aplica los eventos de servers a la réplica de servidores.
// Como con los de usuarios, lo que nunca va a poder procesarse se descarta y un
// error de base se devuelve para que el mensaje se reencole. Todos estos eventos
// se ordenan por occurred_at, así que un occurred_at ausente también se descarta.
type ServerEventsHandler struct {
	sync ServerSync
}

func NewServerEventsHandler(sync ServerSync) *ServerEventsHandler {
	return &ServerEventsHandler{sync: sync}
}

func (h *ServerEventsHandler) Handle(ctx context.Context, routingKey string, body []byte) error {
	switch routingKey {
	case servers.RoutingKeyServerUpdated:
		return h.serverUpdated(ctx, body)
	case servers.RoutingKeyServerDeleted:
		return h.serverDeleted(ctx, body)
	case membership.RoutingKeyMemberJoined:
		return h.membership(ctx, body, true)
	case membership.RoutingKeyMemberLeft:
		return h.membership(ctx, body, false)
	default:
		return Discard(audit.StatusDiscardedUnknownKey, fmt.Errorf("routing key desconocida %q", routingKey))
	}
}

func (h *ServerEventsHandler) serverUpdated(ctx context.Context, body []byte) error {
	var e servers.ServerUpdated
	if err := json.Unmarshal(body, &e); err != nil {
		return Discard(audit.StatusDiscardedMalformed, err)
	}
	serverID, err := parseUUID("server_id", e.ServerID)
	if err != nil {
		return Discard(audit.StatusDiscardedInvalid, err)
	}
	ownerID, err := parseUUID("owner_id", e.OwnerID)
	if err != nil {
		return Discard(audit.StatusDiscardedInvalid, err)
	}
	name := strings.TrimSpace(e.Name)
	if name == "" {
		return Discard(audit.StatusDiscardedInvalid, errors.New("name vacío"))
	}
	if e.CreatedAt.IsZero() {
		return Discard(audit.StatusDiscardedInvalid, errors.New("created_at vacío"))
	}
	if e.OccurredAt.IsZero() {
		return Discard(audit.StatusDiscardedInvalid, errors.New("occurred_at vacío"))
	}
	return h.sync.ApplyServerUpdated(ctx, repository.ServerSnapshot{
		ServerID:   serverID,
		Name:       name,
		OwnerID:    ownerID,
		CreatedAt:  e.CreatedAt.UTC(),
		OccurredAt: e.OccurredAt.UTC(),
	})
}

func (h *ServerEventsHandler) serverDeleted(ctx context.Context, body []byte) error {
	var e servers.ServerDeleted
	if err := json.Unmarshal(body, &e); err != nil {
		return Discard(audit.StatusDiscardedMalformed, err)
	}
	serverID, err := parseUUID("server_id", e.ServerID)
	if err != nil {
		return Discard(audit.StatusDiscardedInvalid, err)
	}
	if e.OccurredAt.IsZero() {
		return Discard(audit.StatusDiscardedInvalid, errors.New("occurred_at vacío"))
	}
	return h.sync.ApplyServerDeleted(ctx, serverID, e.OccurredAt.UTC())
}

// membership atiende member_joined (isMember=true) y member_left (false); los
// dos eventos tienen la misma forma.
func (h *ServerEventsHandler) membership(ctx context.Context, body []byte, isMember bool) error {
	var e membership.MemberJoined
	if err := json.Unmarshal(body, &e); err != nil {
		return Discard(audit.StatusDiscardedMalformed, err)
	}
	serverID, err := parseUUID("server_id", e.ServerID)
	if err != nil {
		return Discard(audit.StatusDiscardedInvalid, err)
	}
	userID, err := parseUUID("user_id", e.UserID)
	if err != nil {
		return Discard(audit.StatusDiscardedInvalid, err)
	}
	if e.OccurredAt.IsZero() {
		return Discard(audit.StatusDiscardedInvalid, errors.New("occurred_at vacío"))
	}
	return h.sync.ApplyMembership(ctx, repository.Membership{
		ServerID:   serverID,
		UserID:     userID,
		IsMember:   isMember,
		OccurredAt: e.OccurredAt.UTC(),
	})
}
