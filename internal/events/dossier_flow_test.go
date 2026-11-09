package events_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/audit"
	"github.com/discordia-grupo01/admin/internal/events"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/admin/internal/service"
	"github.com/discordia-grupo01/admin/internal/testutil"
	"github.com/discordia-grupo01/events-contract/broker"
	"github.com/discordia-grupo01/events-contract/envelope"
	"github.com/discordia-grupo01/events-contract/moderation"
	"github.com/discordia-grupo01/events-contract/reports"
	"github.com/discordia-grupo01/events-contract/servers"
	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Recorre el camino real para los eventos del expediente: RabbitMQ → Consumer →
// ServerEventsHandler → Postgres. Incluye una foto vieja que llega después de la
// nueva, una acción repetida, y el borrado del servidor con eventos tardíos.
func TestDossierEventsFlowThroughRabbitMQ(t *testing.T) {
	url := testutil.RabbitMQURL(t)
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "servers", "server_members", "reports", "moderation_actions", "consumed_events")

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

	queue := "admin.test.dossier." + uuid.NewString()
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler := events.NewServerEventsHandler(service.NewServerService(repository.NewServerRepository(db))).
		WithDossier(service.NewDossierSyncService(repository.NewDossierSyncRepository(db)))
	go events.NewConsumer(url, queue, events.ServerBindings, handler, audit.NewPostgresRecorder(db)).Start(ctx)

	pub := events.NewAMQPPublisher(url)
	defer pub.Close()
	publish := func(key string, payload any) {
		t.Helper()
		if err := pub.Publish(ctx, key, payload); err != nil {
			t.Fatal(err)
		}
	}

	t0 := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	meta := func(min int) envelope.Meta {
		return envelope.Meta{EventID: uuid.NewString(), OccurredAt: t0.Add(time.Duration(min) * time.Minute)}
	}
	serverID, reportID, actionID, target := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	resolvedAt := t0.Add(5 * time.Minute)
	snapshot := func(min int, status string, resolved *time.Time) reports.ReportUpdated {
		return reports.ReportUpdated{
			Meta: meta(min), ReportID: reportID, ServerID: serverID, Kind: reports.KindUser, ReportedUserID: &target,
			Reason: "insultos", Status: status, CreatedAt: t0, ResolvedAt: resolved,
		}
	}

	// La foto nueva (resuelta) llega antes que la vieja (pending): gana la nueva.
	publish(reports.RoutingKeyReportUpdated, snapshot(5, reports.StatusResolved, &resolvedAt))
	publish(reports.RoutingKeyReportUpdated, snapshot(0, reports.StatusPending, nil))
	// La misma acción dos veces deja una sola fila.
	action := moderation.ModerationActionRecorded{Meta: meta(3), ActionID: actionID, ServerID: serverID, Action: moderation.ActionBan, TargetUserID: target}
	publish(moderation.RoutingKeyModerationActionRecorded, action)
	publish(moderation.RoutingKeyModerationActionRecorded, action)

	waitFor(t, "el reporte y la acción se aplican", func() bool {
		return countRows(t, db, `SELECT count(*) FROM reports WHERE report_id = $1 AND status = 'resolved'`, reportID) == 1 &&
			countRows(t, db, `SELECT count(*) FROM moderation_actions WHERE id = $1 AND scope = 'server' AND kind = 'ban'`, actionID) == 1
	})
	waitFor(t, "la cola se vacía", func() bool {
		q, err := ch.QueueDeclarePassive(queue, true, false, false, false, nil)
		return err == nil && q.Messages == 0
	})
	if n := countRows(t, db, `SELECT count(*) FROM moderation_actions`); n != 1 {
		t.Errorf("acciones = %d, la repetida no debió duplicarse", n)
	}

	// Al borrarse el servidor se purga todo, y lo que llegue tarde se descarta.
	publish(servers.RoutingKeyServerDeleted, servers.ServerDeleted{Meta: meta(20), ServerID: serverID})
	waitFor(t, "server_deleted purga el expediente del servidor", func() bool {
		return countRows(t, db, `SELECT count(*) FROM reports`) == 0 && countRows(t, db, `SELECT count(*) FROM moderation_actions`) == 0
	})
	publish(reports.RoutingKeyReportUpdated, snapshot(30, reports.StatusDismissed, &resolvedAt))
	waitFor(t, "el evento tardío se procesa", func() bool {
		return countRows(t, db, `SELECT count(*) FROM consumed_events WHERE routing_key = $1`, reports.RoutingKeyReportUpdated) >= 3
	})
	if n := countRows(t, db, `SELECT count(*) FROM reports`); n != 0 {
		t.Errorf("reportes = %d, un evento tardío no debe resucitar un servidor borrado", n)
	}
}
