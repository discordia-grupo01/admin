package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/discordia-grupo01/admin/internal/audit"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/events-contract/users"
	"github.com/google/uuid"
)

// QueueUsers es la cola durable de admin para la réplica de usuarios.
const QueueUsers = "admin.users"

// UserBindings son las routing keys que alimentan la réplica de usuarios.
var UserBindings = []string{users.RoutingKeyAccountRegistered, users.RoutingKeyProfileUpdated}

// UserSync es lo que el handler necesita para aplicar los eventos de usuarios.
type UserSync interface {
	ApplyRegistration(ctx context.Context, r repository.Registration) error
	ApplyProfile(ctx context.Context, p repository.Profile) error
}

// UserEventsHandler aplica users.account_registered y users.profile_updated a
// la réplica de usuarios. Los mensajes que nunca van a poder procesarse
// (JSON roto, ids inválidos, clave desconocida) se descartan; un error de
// base se devuelve tal cual para que el mensaje se reencole.
type UserEventsHandler struct {
	sync UserSync
}

func NewUserEventsHandler(sync UserSync) *UserEventsHandler {
	return &UserEventsHandler{sync: sync}
}

func (h *UserEventsHandler) Handle(ctx context.Context, routingKey string, body []byte) error {
	switch routingKey {
	case users.RoutingKeyAccountRegistered:
		return h.accountRegistered(ctx, body)
	case users.RoutingKeyProfileUpdated:
		return h.profileUpdated(ctx, body)
	default:
		return Discard(audit.StatusDiscardedUnknownKey, fmt.Errorf("routing key desconocida %q", routingKey))
	}
}

func (h *UserEventsHandler) accountRegistered(ctx context.Context, body []byte) error {
	var e users.AccountRegistered
	if err := json.Unmarshal(body, &e); err != nil {
		return Discard(audit.StatusDiscardedMalformed, err)
	}
	id, err := parseUserID(e.UserID)
	if err != nil {
		return Discard(audit.StatusDiscardedInvalid, err)
	}
	email := strings.TrimSpace(e.Email)
	if email == "" {
		return Discard(audit.StatusDiscardedInvalid, errors.New("email vacío"))
	}
	if e.CreatedAt.IsZero() {
		return Discard(audit.StatusDiscardedInvalid, errors.New("created_at vacío"))
	}
	return h.sync.ApplyRegistration(ctx, repository.Registration{
		UserID:       id,
		Email:        email,
		Name:         e.Name,
		RegisteredAt: e.CreatedAt.UTC(),
	})
}

func (h *UserEventsHandler) profileUpdated(ctx context.Context, body []byte) error {
	var e users.ProfileUpdated
	if err := json.Unmarshal(body, &e); err != nil {
		return Discard(audit.StatusDiscardedMalformed, err)
	}
	id, err := parseUserID(e.UserID)
	if err != nil {
		return Discard(audit.StatusDiscardedInvalid, err)
	}
	// identify arranca profile_version en 1; 0 queda reservado para "el nombre
	// vino del registro" (ver UpsertRegistration), así que una foto con
	// version < 1 no tiene sentido.
	if e.Version < 1 {
		return Discard(audit.StatusDiscardedInvalid, fmt.Errorf("version %d inválida: debe ser >= 1", e.Version))
	}
	return h.sync.ApplyProfile(ctx, repository.Profile{UserID: id, Name: e.Name, Version: e.Version})
}

func parseUserID(raw string) (string, error) { return parseUUID("user_id", raw) }

// parseUUID valida que el id sea un UUID (la columna lo es: uno inválido haría
// fallar el INSERT para siempre y el mensaje se reintentaría sin fin) y lo
// devuelve en su forma canónica.
func parseUUID(field, raw string) (string, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%s %q no es un UUID: %w", field, raw, err)
	}
	return id.String(), nil
}
