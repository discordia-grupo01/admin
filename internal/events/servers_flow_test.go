package events_test

import (
	"context"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/audit"
	"github.com/discordia-grupo01/admin/internal/events"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/admin/internal/service"
	"github.com/discordia-grupo01/admin/internal/testutil"
	"github.com/discordia-grupo01/events-contract/broker"
	"github.com/discordia-grupo01/events-contract/envelope"
	"github.com/discordia-grupo01/events-contract/membership"
	"github.com/discordia-grupo01/events-contract/servers"
	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

// Recorre el camino real: RabbitMQ → Consumer → ServerEventsHandler → servicio →
// Postgres → listado. Incluye eventos fuera de orden y repetidos, un mensaje
// inválido y el borrado del servidor.
func TestServerEventsFlowThroughRabbitMQ(t *testing.T) {
	url := testutil.RabbitMQURL(t)
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "servers", "server_members", "users", "consumed_events")

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

	queue := "admin.test.servers." + uuid.NewString()
	if err := ch.ExchangeDeclare(broker.ExchangeName, broker.ExchangeType, true, false, false, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		t.Fatal(err)
	}
	defer ch.QueueDelete(queue, false, false, false)
	for _, key := range events.ServerBindings {
		if err := ch.QueueBind(queue, key, broker.ExchangeName, false, nil); err != nil {
			t.Fatal(err)
		}
	}

	serverRepo := repository.NewServerRepository(db)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go events.NewConsumer(url, queue, events.ServerBindings,
		events.NewServerEventsHandler(service.NewServerService(serverRepo)), audit.NewPostgresRecorder(db)).Start(ctx)

	pub := events.NewAMQPPublisher(url)
	defer pub.Close()
	publish := func(key string, payload any) {
		t.Helper()
		if err := pub.Publish(ctx, key, payload); err != nil {
			t.Fatal(err)
		}
	}

	t0 := time.Date(2026, 9, 26, 14, 0, 0, 0, time.UTC)
	meta := func(min int) envelope.Meta {
		return envelope.Meta{EventID: uuid.NewString(), OccurredAt: t0.Add(time.Duration(min) * time.Minute)}
	}
	id, owner, alice, bob := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()

	// Las membresías llegan antes que la foto del servidor, y alice se va después de entrar.
	publish(membership.RoutingKeyMemberJoined, membership.MemberJoined{Meta: meta(2), ServerID: id, UserID: alice})
	publish(membership.RoutingKeyMemberJoined, membership.MemberJoined{Meta: meta(3), ServerID: id, UserID: bob})
	publish(membership.RoutingKeyMemberLeft, membership.MemberLeft{Meta: meta(4), ServerID: id, UserID: alice})
	publish(membership.RoutingKeyMemberJoined, membership.MemberJoined{Meta: meta(1), ServerID: id, UserID: alice}) // tardío: no revive a alice
	// Mensaje inválido: se descarta sin trabar la cola.
	if err := ch.PublishWithContext(ctx, broker.ExchangeName, servers.RoutingKeyServerUpdated, false, false,
		amqp.Publishing{ContentType: "application/json", Body: []byte("esto no es json"), DeliveryMode: amqp.Persistent}); err != nil {
		t.Fatal(err)
	}
	// Foto del servidor: la nueva llega antes que la vieja, y se repite.
	newer := servers.ServerUpdated{Meta: meta(10), ServerID: id, Name: "Nombre nuevo", OwnerID: owner, CreatedAt: t0}
	publish(servers.RoutingKeyServerUpdated, newer)
	publish(servers.RoutingKeyServerUpdated, servers.ServerUpdated{Meta: meta(5), ServerID: id, Name: "Nombre viejo", OwnerID: owner, CreatedAt: t0})
	publish(servers.RoutingKeyServerUpdated, newer)

	waitFor(t, "el servidor aparece con sus miembros", func() bool {
		list, _, err := serverRepo.List(ctx, repository.ServerFilter{}, 10, 0)
		return err == nil && len(list) == 1 && list[0].MemberCount == 1
	})
	list, total, err := serverRepo.List(ctx, repository.ServerFilter{Search: "nuevo"}, 10, 0)
	if err != nil || total != 1 {
		t.Fatalf("búsqueda por nombre: total=%d err=%v", total, err)
	}
	if s := list[0]; s.ID != id || s.Name != "Nombre nuevo" || s.OwnerID != owner || s.MemberCount != 1 || !s.CreatedAt.Equal(t0) {
		t.Errorf("servidor: %+v (debe ganar la foto más nueva, y alice no debe contarse)", s)
	}
	waitFor(t, "auditoría del mensaje inválido", func() bool {
		return countAudit(t, db, audit.StatusDiscardedMalformed) == 1
	})

	// El borrado lo saca del listado.
	publish(servers.RoutingKeyServerDeleted, servers.ServerDeleted{Meta: meta(20), ServerID: id})
	waitFor(t, "el servidor desaparece del listado", func() bool {
		_, total, err := serverRepo.List(ctx, repository.ServerFilter{}, 10, 0)
		return err == nil && total == 0
	})
	waitFor(t, "la cola se vacía (nada quedó reencolado)", func() bool {
		q, err := ch.QueueDeclarePassive(queue, true, false, false, false, nil)
		return err == nil && q.Messages == 0
	})
}
