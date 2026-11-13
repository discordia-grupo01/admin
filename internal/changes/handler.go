package changes

import (
	"context"

	"github.com/discordia-grupo01/events-contract/membership"
	"github.com/discordia-grupo01/events-contract/messages"
	"github.com/discordia-grupo01/events-contract/moderation"
	"github.com/discordia-grupo01/events-contract/reports"
	"github.com/discordia-grupo01/events-contract/servers"
	"github.com/discordia-grupo01/events-contract/users"
)

type EventHandler interface {
	Handle(ctx context.Context, routingKey string, body []byte) error
}

var resourcesOf = map[string][]string{
	users.RoutingKeyAccountRegistered:             {Users, Metrics},
	users.RoutingKeyProfileUpdated:                {Users},
	users.RoutingKeyEmailVerified:                 {Users},
	users.RoutingKeyAccountRecoveryRequested:      {RecoveryRequests, Dossier},
	servers.RoutingKeyServerUpdated:               {Servers},
	servers.RoutingKeyServerDeleted:               {Servers, Metrics},
	membership.RoutingKeyMemberJoined:             {Servers, Metrics},
	membership.RoutingKeyMemberLeft:               {Servers, Metrics},
	reports.RoutingKeyReportUpdated:               {Reports, Dossier},
	moderation.RoutingKeyModerationActionRecorded: {Dossier},
	messages.RoutingKeyMessageCreated:             {Metrics},
}

type notifying struct {
	inner    EventHandler
	notifier Notifier
}

func Notifying(inner EventHandler, notifier Notifier) EventHandler {
	return &notifying{inner: inner, notifier: notifier}
}

func (h *notifying) Handle(ctx context.Context, routingKey string, body []byte) error {
	if err := h.inner.Handle(ctx, routingKey, body); err != nil {
		return err
	}
	if resources, ok := resourcesOf[routingKey]; ok {
		h.notifier.Changed(ctx, resources...)
	}
	return nil
}
