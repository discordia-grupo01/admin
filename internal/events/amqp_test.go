package events_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/audit"
	"github.com/discordia-grupo01/admin/internal/events"
	"github.com/discordia-grupo01/admin/internal/testutil"
	"github.com/google/uuid"
)

type captureHandler struct{ got chan string }

func (h captureHandler) Handle(_ context.Context, routingKey string, _ []byte) error {
	h.got <- routingKey
	return nil
}

type nopRecorder struct{}

func (nopRecorder) Record(context.Context, audit.Entry) {}

// Publica con AMQPPublisher y lo recibe con Consumer, a través del RabbitMQ real.
func TestPublisherToConsumerRoundTrip(t *testing.T) {
	url := testutil.RabbitMQURL(t)

	pub := events.NewAMQPPublisher(url)
	defer pub.Close()
	if err := pub.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	// Cola y routing key únicas para no cruzarse con otras corridas.
	suffix := uuid.NewString()
	queue := "admin.test." + suffix
	key := "admin.test_event_" + suffix

	h := captureHandler{got: make(chan string, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go events.NewConsumer(url, queue, []string{key}, h, nopRecorder{}).Start(ctx)

	deadline := time.After(10 * time.Second)
	for {
		// Hasta que la cola esté declarada y enlazada, el publish se pierde: se
		// reintenta hasta que llegue.
		if err := pub.Publish(ctx, key, json.RawMessage(`{"event_id":"e1"}`)); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		select {
		case got := <-h.got:
			if got != key {
				t.Fatalf("routing key = %q, quería %q", got, key)
			}
			return
		case <-time.After(200 * time.Millisecond):
		case <-deadline:
			t.Fatal("el consumer no recibió el evento")
		}
	}
}

func TestPublishFailsWhenBrokerUnreachable(t *testing.T) {
	pub := events.NewAMQPPublisher("amqp://guest:guest@127.0.0.1:1/")
	defer pub.Close()
	if err := pub.Publish(context.Background(), "k", map[string]string{}); err == nil {
		t.Fatal("esperaba error con el broker inaccesible")
	}
	if err := pub.Ping(); err == nil {
		t.Fatal("Ping debería fallar con el broker inaccesible")
	}
}

func TestPublishRejectsUnserializablePayload(t *testing.T) {
	pub := events.NewAMQPPublisher("amqp://guest:guest@127.0.0.1:1/")
	if err := pub.Publish(context.Background(), "k", make(chan int)); err == nil {
		t.Fatal("esperaba error al serializar un canal")
	}
}
