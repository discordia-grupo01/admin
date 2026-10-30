package events_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/audit"
	"github.com/discordia-grupo01/admin/internal/events"
	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/admin/internal/service"
	"github.com/discordia-grupo01/admin/internal/testutil"
	"github.com/discordia-grupo01/events-contract/broker"
	"github.com/discordia-grupo01/events-contract/users"
	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout esperando: %s", what)
}

func countAudit(t *testing.T, db *sql.DB, status audit.Status) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM consumed_events WHERE status = $1`, string(status)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Recorre el camino real: RabbitMQ → Consumer → UserEventsHandler → servicio →
// Postgres → listado. Incluye un mensaje malformado y uno con id inválido, que
// no deben trabar la cola ni reintentarse para siempre.
func TestUserEventsFlowThroughRabbitMQ(t *testing.T) {
	url := testutil.RabbitMQURL(t)
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "users", "consumed_events")

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

	// Cola propia del test, creada y enlazada antes de publicar para no perder nada.
	queue := "admin.test.users." + uuid.NewString()
	if err := ch.ExchangeDeclare(broker.ExchangeName, broker.ExchangeType, true, false, false, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		t.Fatal(err)
	}
	defer ch.QueueDelete(queue, false, false, false)
	for _, key := range events.UserBindings {
		if err := ch.QueueBind(queue, key, broker.ExchangeName, false, nil); err != nil {
			t.Fatal(err)
		}
	}

	userRepo := repository.NewUserRepository(db)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go events.NewConsumer(url, queue, events.UserBindings,
		events.NewUserEventsHandler(service.NewUserService(userRepo)), audit.NewPostgresRecorder(db)).Start(ctx)

	pub := events.NewAMQPPublisher(url)
	defer pub.Close()
	id := uuid.NewString()
	raw := func(key, body string) {
		t.Helper()
		if err := ch.PublishWithContext(ctx, broker.ExchangeName, key, false, false,
			amqp.Publishing{ContentType: "application/json", Body: []byte(body), DeliveryMode: amqp.Persistent}); err != nil {
			t.Fatal(err)
		}
	}

	// 1) El perfil llega ANTES que el alta, y además fuera de orden.
	publish := func(key string, payload any) {
		t.Helper()
		if err := pub.Publish(ctx, key, payload); err != nil {
			t.Fatal(err)
		}
	}
	publish(users.RoutingKeyProfileUpdated, users.ProfileUpdated{UserID: id, Version: 3, Name: "Nombre v3"})
	publish(users.RoutingKeyProfileUpdated, users.ProfileUpdated{UserID: id, Version: 2, Name: "Nombre v2 tardío"})
	// 2) Mensajes que no se pueden procesar nunca.
	raw(users.RoutingKeyProfileUpdated, "esto no es json")
	raw(users.RoutingKeyAccountRegistered, `{"user_id":"no-es-uuid","email":"x@y.z","created_at":"2026-09-26T14:30:00Z"}`)
	// 3) La verificación del email, también antes del alta.
	publish(users.RoutingKeyEmailVerified, users.EmailVerified{UserID: id, VerifiedAt: time.Date(2026, 9, 26, 14, 35, 0, 0, time.UTC)})
	// 4) El alta, repetida (at-least-once), sin email_verified: no pisa la
	// verificación que ya llegó.
	registered := users.AccountRegistered{UserID: id, Email: "manu@example.com", Name: "Nombre del registro", CreatedAt: time.Date(2026, 9, 26, 14, 30, 0, 0, time.UTC)}
	publish(users.RoutingKeyAccountRegistered, registered)
	publish(users.RoutingKeyAccountRegistered, registered)

	waitFor(t, "el usuario aparece en el listado", func() bool {
		list, _, err := userRepo.List(ctx, repository.UserFilter{}, 10, 0)
		return err == nil && len(list) == 1
	})
	list, total, err := userRepo.List(ctx, repository.UserFilter{Search: "manu@"}, 10, 0)
	if err != nil || total != 1 {
		t.Fatalf("búsqueda por email: total=%d err=%v", total, err)
	}
	u := list[0]
	if u.ID != id || u.Email != "manu@example.com" || u.Status != model.UserStatusActive {
		t.Errorf("usuario: %+v (debe estar activo: la verificación llegó antes que el alta)", u)
	}
	if u.Name != "Nombre v3" {
		t.Errorf("name = %q: debe ganar la versión más nueva del perfil, aunque llegó antes del alta y la v2 llegó tarde", u.Name)
	}

	// Los dos mensajes inválidos se descartaron y quedaron auditados, sin reencolarse.
	waitFor(t, "auditoría de los descartes", func() bool {
		return countAudit(t, db, audit.StatusDiscardedMalformed) == 1 && countAudit(t, db, audit.StatusDiscardedInvalid) == 1
	})
	waitFor(t, "la cola se vacía (nada quedó reencolado)", func() bool {
		q, err := ch.QueueDeclarePassive(queue, true, false, false, false, nil)
		return err == nil && q.Messages == 0
	})
}
