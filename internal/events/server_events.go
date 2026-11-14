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
	"github.com/discordia-grupo01/events-contract/moderation"
	"github.com/discordia-grupo01/events-contract/reports"
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
	// Alimentan el expediente de investigación (HU #81).
	reports.RoutingKeyReportUpdated,
	moderation.RoutingKeyModerationActionRecorded,
}

// ServerSync es lo que el handler necesita para aplicar los eventos de servidores.
type ServerSync interface {
	ApplyServerUpdated(ctx context.Context, s repository.ServerSnapshot) error
	ApplyServerDeleted(ctx context.Context, serverID string, at time.Time) error
	ApplyMembership(ctx context.Context, m repository.Membership) error
}

// DossierSync es lo que el handler necesita para aplicar los eventos que
// alimentan el expediente.
type DossierSync interface {
	ApplyReportUpdated(ctx context.Context, r repository.ReportSnapshot) error
	ApplyModerationActionRecorded(ctx context.Context, a repository.ServerModerationAction) error
}

// ServerEventsHandler aplica los eventos de servers a la réplica de servidores.
// Como con los de usuarios, lo que nunca va a poder procesarse se descarta y un
// error de base se devuelve para que el mensaje se reencole. Todos estos eventos
// se ordenan por occurred_at, así que un occurred_at ausente también se descarta.
type ServerEventsHandler struct {
	sync    ServerSync
	dossier DossierSync
}

func NewServerEventsHandler(sync ServerSync) *ServerEventsHandler {
	return &ServerEventsHandler{sync: sync}
}

// WithDossier habilita los eventos del expediente (reportes y acciones de
// moderación). Sin esto se descartan como routing keys desconocidas.
func (h *ServerEventsHandler) WithDossier(d DossierSync) *ServerEventsHandler {
	h.dossier = d
	return h
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
	case reports.RoutingKeyReportUpdated:
		if h.dossier == nil {
			break
		}
		return h.reportUpdated(ctx, body)
	case moderation.RoutingKeyModerationActionRecorded:
		if h.dossier == nil {
			break
		}
		return h.moderationActionRecorded(ctx, body)
	}
	return Discard(audit.StatusDiscardedUnknownKey, fmt.Errorf("routing key desconocida %q", routingKey))
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
		IconURL:    strings.TrimSpace(e.IconURL),
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
		EventID:    e.EventID,
		ServerID:   serverID,
		UserID:     userID,
		IsMember:   isMember,
		OccurredAt: e.OccurredAt.UTC(),
	})
}

// optionalUUID valida un id que puede venir nulo. Devuelve "" si no hay valor.
func optionalUUID(field string, raw *string) (string, error) {
	if raw == nil || *raw == "" {
		return "", nil
	}
	return parseUUID(field, *raw)
}

func (h *ServerEventsHandler) reportUpdated(ctx context.Context, body []byte) error {
	var e reports.ReportUpdated
	if err := json.Unmarshal(body, &e); err != nil {
		return Discard(audit.StatusDiscardedMalformed, err)
	}
	reportID, err := parseUUID("report_id", e.ReportID)
	if err != nil {
		return Discard(audit.StatusDiscardedInvalid, err)
	}
	serverID, err := parseUUID("server_id", e.ServerID)
	if err != nil {
		return Discard(audit.StatusDiscardedInvalid, err)
	}
	if e.Kind != reports.KindUser && e.Kind != reports.KindMessage {
		return Discard(audit.StatusDiscardedInvalid, fmt.Errorf("kind %q desconocido", e.Kind))
	}
	switch e.Status {
	case reports.StatusPending, reports.StatusResolved, reports.StatusDismissed:
	default:
		return Discard(audit.StatusDiscardedInvalid, fmt.Errorf("status %q desconocido", e.Status))
	}
	reportedUserID, err := optionalUUID("reported_user_id", e.ReportedUserID)
	if err != nil {
		return Discard(audit.StatusDiscardedInvalid, err)
	}
	channelID, err := optionalUUID("channel_id", e.ChannelID)
	if err != nil {
		return Discard(audit.StatusDiscardedInvalid, err)
	}
	messageID, err := optionalUUID("message_id", e.MessageID)
	if err != nil {
		return Discard(audit.StatusDiscardedInvalid, err)
	}
	if e.CreatedAt.IsZero() {
		return Discard(audit.StatusDiscardedInvalid, errors.New("created_at vacío"))
	}
	if e.OccurredAt.IsZero() {
		return Discard(audit.StatusDiscardedInvalid, errors.New("occurred_at vacío"))
	}
	var resolvedAt *time.Time
	if e.ResolvedAt != nil {
		t := e.ResolvedAt.UTC()
		resolvedAt = &t
	}
	return h.dossier.ApplyReportUpdated(ctx, repository.ReportSnapshot{
		ReportID:       reportID,
		ServerID:       serverID,
		Kind:           e.Kind,
		ReportedUserID: reportedUserID,
		ChannelID:      channelID,
		MessageID:      messageID,
		Reason:         e.Reason,
		Status:         e.Status,
		CreatedAt:      e.CreatedAt.UTC(),
		ResolvedAt:     resolvedAt,
		OccurredAt:     e.OccurredAt.UTC(),
	})
}

func (h *ServerEventsHandler) moderationActionRecorded(ctx context.Context, body []byte) error {
	var e moderation.ModerationActionRecorded
	if err := json.Unmarshal(body, &e); err != nil {
		return Discard(audit.StatusDiscardedMalformed, err)
	}
	actionID, err := parseUUID("action_id", e.ActionID)
	if err != nil {
		return Discard(audit.StatusDiscardedInvalid, err)
	}
	serverID, err := parseUUID("server_id", e.ServerID)
	if err != nil {
		return Discard(audit.StatusDiscardedInvalid, err)
	}
	targetID, err := parseUUID("target_user_id", e.TargetUserID)
	if err != nil {
		return Discard(audit.StatusDiscardedInvalid, err)
	}
	switch e.Action {
	case moderation.ActionBan, moderation.ActionUnban, moderation.ActionKick, moderation.ActionMute, moderation.ActionUnmute:
	default:
		return Discard(audit.StatusDiscardedInvalid, fmt.Errorf("action %q desconocida", e.Action))
	}
	if e.OccurredAt.IsZero() {
		return Discard(audit.StatusDiscardedInvalid, errors.New("occurred_at vacío"))
	}
	return h.dossier.ApplyModerationActionRecorded(ctx, repository.ServerModerationAction{
		ActionID:     actionID,
		ServerID:     serverID,
		Kind:         e.Action,
		TargetUserID: targetID,
		OccurredAt:   e.OccurredAt.UTC(),
	})
}
