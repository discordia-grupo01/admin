package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/discordia-grupo01/admin/internal/audit"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/events-contract/messages"
)

// QueueActivity es la cola durable de admin para la actividad de mensajes del
// expediente. Es aparte de admin.servers para tener su propia DLQ y porque el
// volumen (un evento por mensaje) no debe competir con la réplica de servidores.
const QueueActivity = "admin.activity"

// ActivityBindings son las routing keys de la cola de actividad.
var ActivityBindings = []string{
	messages.RoutingKeyMessageCreated,
}

// ActivitySync es lo que el handler necesita para registrar la actividad.
type ActivitySync interface {
	ApplyMessageCreated(ctx context.Context, m repository.MessageActivity) error
}

// ActivityEventsHandler registra cada mensaje enviado como actividad del autor.
// Descarta content_preview y las menciones: admin guarda dónde y cuándo escribió
// alguien, nunca qué.
type ActivityEventsHandler struct {
	sync ActivitySync
}

func NewActivityEventsHandler(sync ActivitySync) *ActivityEventsHandler {
	return &ActivityEventsHandler{sync: sync}
}

func (h *ActivityEventsHandler) Handle(ctx context.Context, routingKey string, body []byte) error {
	if routingKey != messages.RoutingKeyMessageCreated {
		return Discard(audit.StatusDiscardedUnknownKey, fmt.Errorf("routing key desconocida %q", routingKey))
	}
	var e messages.MessageCreated
	if err := json.Unmarshal(body, &e); err != nil {
		return Discard(audit.StatusDiscardedMalformed, err)
	}
	eventID, err := parseUUID("event_id", e.EventID)
	if err != nil {
		return Discard(audit.StatusDiscardedInvalid, err)
	}
	userID, err := parseUUID("author_id", e.AuthorID)
	if err != nil {
		return Discard(audit.StatusDiscardedInvalid, err)
	}
	serverID, err := parseUUID("server_id", e.ServerID)
	if err != nil {
		return Discard(audit.StatusDiscardedInvalid, err)
	}
	channelID, err := parseUUID("channel_id", e.ChannelID)
	if err != nil {
		return Discard(audit.StatusDiscardedInvalid, err)
	}
	messageID, err := parseUUID("message_id", e.MessageID)
	if err != nil {
		return Discard(audit.StatusDiscardedInvalid, err)
	}
	if e.OccurredAt.IsZero() {
		return Discard(audit.StatusDiscardedInvalid, errors.New("occurred_at vacío"))
	}
	return h.sync.ApplyMessageCreated(ctx, repository.MessageActivity{
		EventID:     eventID,
		UserID:      userID,
		ServerID:    serverID,
		ChannelID:   channelID,
		ChannelName: e.ChannelName,
		MessageID:   messageID,
		OccurredAt:  e.OccurredAt.UTC(),
	})
}
