package events_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/audit"
	"github.com/discordia-grupo01/admin/internal/events"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/admin/internal/service"
	"github.com/discordia-grupo01/admin/internal/testutil"
	"github.com/discordia-grupo01/events-contract/broker"
	"github.com/discordia-grupo01/events-contract/envelope"
	"github.com/discordia-grupo01/events-contract/messages"
	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

// Recorre RabbitMQ → Consumer → ActivityEventsHandler → Postgres con el mismo
// armado que main.go (auditoría sin cuerpo) y fija que el texto del mensaje no
// queda en NINGUNA tabla de admin, ni siquiera en la auditoría de un descarte.
func TestMessageActivityFlowNeverStoresTheMessageText(t *testing.T) {
	url := testutil.RabbitMQURL(t)
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "servers", "user_activity", "consumed_events", "outbox_events")

	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()

	queue := "admin.test.activity." + uuid.NewString()
	if err := ch.ExchangeDeclare(broker.ExchangeName, broker.ExchangeType, true, false, false, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		t.Fatal(err)
	}
	defer ch.QueueDelete(queue, false, false, false)
	for _, key := range events.ActivityBindings {
		if err := ch.QueueBind(queue, key, broker.ExchangeName, false, nil); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler := events.NewActivityEventsHandler(service.NewActivityService(repository.NewActivityRepository(db)))
	go events.NewConsumer(url, queue, events.ActivityBindings, handler, audit.WithoutBody(audit.NewPostgresRecorder(db))).Start(ctx)

	pub := events.NewAMQPPublisher(url)
	defer pub.Close()

	const secret = "contraseña-del-wifi-es-hunter2"
	t0 := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	author, serverID := uuid.NewString(), uuid.NewString()
	msg := func(min int, mutate func(*messages.MessageCreated)) messages.MessageCreated {
		m := messages.MessageCreated{
			Meta:     envelope.Meta{EventID: uuid.NewString(), OccurredAt: t0.Add(time.Duration(min) * time.Minute)},
			ServerID: serverID, ChannelID: uuid.NewString(), MessageID: uuid.NewString(), AuthorID: author,
			MentionedUserIDs: []string{uuid.NewString()}, ChannelName: "general", ContentPreview: secret,
		}
		if mutate != nil {
			mutate(&m)
		}
		return m
	}

	first := msg(0, nil)
	for i := 0; i < 2; i++ { // el mismo evento dos veces: una sola fila
		if err := pub.Publish(ctx, messages.RoutingKeyMessageCreated, first); err != nil {
			t.Fatal(err)
		}
	}
	if err := pub.Publish(ctx, messages.RoutingKeyMessageCreated, msg(1, nil)); err != nil {
		t.Fatal(err)
	}
	// Un evento inválido (con el texto adentro) se descarta y se audita, sin cuerpo.
	if err := pub.Publish(ctx, messages.RoutingKeyMessageCreated, msg(2, func(m *messages.MessageCreated) { m.AuthorID = "no-es-uuid" })); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "dos mensajes registrados y el inválido auditado", func() bool {
		return countRows(t, db, `SELECT count(*) FROM user_activity WHERE user_id = $1 AND kind = 'message_sent'`, author) == 2 &&
			countAudit(t, db, audit.StatusDiscardedInvalid) == 1
	})
	waitFor(t, "la cola se vacía", func() bool {
		q, err := ch.QueueDeclarePassive(queue, true, false, false, false, nil)
		return err == nil && q.Messages == 0
	})

	// Los procesados no dejan fila en consumed_events: solo el descarte.
	if n := countRows(t, db, `SELECT count(*) FROM consumed_events`); n != 1 {
		t.Errorf("consumed_events = %d, quería solo el descarte", n)
	}

	// Ninguna tabla de admin contiene el texto.
	rows, err := db.Query(`SELECT tablename FROM pg_tables WHERE schemaname = current_schema()`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	for _, table := range tables {
		var dump string
		if err := db.QueryRow(`SELECT COALESCE(string_agg(t::text, E'\n'), '') FROM "` + table + `" t`).Scan(&dump); err != nil {
			t.Fatalf("leer %s: %v", table, err)
		}
		if strings.Contains(dump, secret) || strings.Contains(dump, "hunter2") {
			t.Errorf("la tabla %s guardó el texto del mensaje", table)
		}
	}
}
