package events_test

import (
	"context"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/discordia-grupo01/admin/internal/events"
	"github.com/discordia-grupo01/admin/internal/testutil"
)

func TestAMQPPublisherCarriesTheTraceInTheHeaders(t *testing.T) {
	url := testutil.RabbitMQURL(t)

	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatalf("amqp.Dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("conn.Channel: %v", err)
	}
	t.Cleanup(func() { ch.Close() })
	if err := ch.ExchangeDeclare("discordia.events", "topic", true, false, false, false, nil); err != nil {
		t.Fatalf("ExchangeDeclare: %v", err)
	}
	q, err := ch.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		t.Fatalf("QueueDeclare: %v", err)
	}
	if err := ch.QueueBind(q.Name, "admin.traced_header", "discordia.events", false, nil); err != nil {
		t.Fatalf("QueueBind: %v", err)
	}
	deliveries, err := ch.Consume(q.Name, "", true, true, false, false, nil)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}

	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(sdktrace.NewTracerProvider())
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})

	ctx, span := otel.Tracer("test").Start(context.Background(), "publish")
	defer span.End()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := events.NewAMQPPublisher(url).Publish(ctx, "admin.traced_header", map[string]string{"k": "v"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case msg := <-deliveries:
		got, _ := msg.Headers["traceparent"].(string)
		if !strings.Contains(got, span.SpanContext().TraceID().String()) {
			t.Errorf("traceparent = %q, quería la traza %s", got, span.SpanContext().TraceID())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("se agotó el tiempo esperando el mensaje")
	}
}
