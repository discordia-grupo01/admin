package events

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/audit"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/events-contract/messages"
)

type fakeActivitySync struct {
	messages []repository.MessageActivity
	err      error
}

func (f *fakeActivitySync) ApplyMessageCreated(_ context.Context, m repository.MessageActivity) error {
	f.messages = append(f.messages, m)
	return f.err
}

const (
	eventUUID   = "11111111-2222-4333-8444-555555555555"
	channelUUID = "66666666-7777-4888-9999-000000000000"
	messageUUID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
)

func messageBody(over map[string]string) []byte {
	fields := map[string]string{
		"event_id": `"` + eventUUID + `"`, "occurred_at": `"2026-11-02T10:00:00Z"`, "server_id": `"` + serverID + `"`,
		"channel_id": `"` + channelUUID + `"`, "message_id": `"` + messageUUID + `"`, "author_id": `"` + userID + `"`,
		"channel_name": `"general"`, "content_preview": `"hola, esto es privado"`, "mentioned_user_ids": `["` + otherID + `"]`,
	}
	for k, v := range over {
		fields[k] = v
	}
	b := "{"
	first := true
	for k, v := range fields {
		if v == "" {
			continue
		}
		if !first {
			b += ","
		}
		first = false
		b += `"` + k + `":` + v
	}
	return []byte(b + "}")
}

func TestMessageCreatedIsAppliedWithoutItsText(t *testing.T) {
	sync := &fakeActivitySync{}
	if err := NewActivityEventsHandler(sync).Handle(context.Background(), messages.RoutingKeyMessageCreated, messageBody(nil)); err != nil {
		t.Fatal(err)
	}
	want := repository.MessageActivity{
		EventID: eventUUID, UserID: userID, ServerID: serverID, ChannelID: channelUUID, ChannelName: "general",
		MessageID: messageUUID, OccurredAt: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC),
	}
	if len(sync.messages) != 1 || sync.messages[0] != want {
		t.Errorf("aplicado = %+v, quería %+v", sync.messages, want)
	}
}

func TestMessageCreatedWithoutChannelNameIsApplied(t *testing.T) {
	sync := &fakeActivitySync{}
	if err := NewActivityEventsHandler(sync).Handle(context.Background(), messages.RoutingKeyMessageCreated, messageBody(map[string]string{"channel_name": ""})); err != nil {
		t.Fatal(err)
	}
	if len(sync.messages) != 1 || sync.messages[0].ChannelName != "" {
		t.Errorf("aplicado = %+v", sync.messages)
	}
}

func TestInvalidMessageCreatedIsDiscarded(t *testing.T) {
	cases := map[string]map[string]string{
		"event_id inválido":   {"event_id": `"x"`},
		"author_id inválido":  {"author_id": `"x"`},
		"server_id inválido":  {"server_id": `"x"`},
		"channel_id inválido": {"channel_id": `"x"`},
		"message_id inválido": {"message_id": `"x"`},
		"sin occurred_at":     {"occurred_at": `"0001-01-01T00:00:00Z"`},
	}
	for name, over := range cases {
		t.Run(name, func(t *testing.T) {
			sync := &fakeActivitySync{}
			err := NewActivityEventsHandler(sync).Handle(context.Background(), messages.RoutingKeyMessageCreated, messageBody(over))
			assertDiscarded(t, err, audit.StatusDiscardedInvalid)
			if len(sync.messages) != 0 {
				t.Error("no debió aplicar nada")
			}
		})
	}
	h := NewActivityEventsHandler(&fakeActivitySync{})
	assertDiscarded(t, h.Handle(context.Background(), messages.RoutingKeyMessageCreated, []byte(`{`)), audit.StatusDiscardedMalformed)
	assertDiscarded(t, h.Handle(context.Background(), "messaging.otra_cosa", []byte(`{}`)), audit.StatusDiscardedUnknownKey)
}

func TestMessageCreatedDatabaseErrorIsTransient(t *testing.T) {
	boom := errors.New("db caída")
	err := NewActivityEventsHandler(&fakeActivitySync{err: boom}).Handle(context.Background(), messages.RoutingKeyMessageCreated, messageBody(nil))
	var d *DiscardError
	if !errors.Is(err, boom) || errors.As(err, &d) {
		t.Errorf("err = %v, quería el error de base sin descartar", err)
	}
}
