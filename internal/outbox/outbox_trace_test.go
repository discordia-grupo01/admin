package outbox_test

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/discordia-grupo01/admin/internal/outbox"
	"github.com/discordia-grupo01/admin/internal/testutil"
)

func tracing(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	spans := tracetest.NewSpanRecorder()
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})
	return spans
}

func TestInsertStoresTheTraceparentAndDispatchReturnsIt(t *testing.T) {
	tracing(t)
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "outbox_events")
	ctx, span := otel.Tracer("test").Start(context.Background(), "request")
	defer span.End()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := outbox.Insert(ctx, tx, "admin.with_trace", map[string]string{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	if err := outbox.Insert(context.Background(), tx, "admin.without_trace", map[string]string{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	if _, err := outbox.NewPostgresStore(db).Dispatch(context.Background(), 10, func(_ context.Context, e outbox.Event) error {
		got[e.RoutingKey] = e.Traceparent
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	want := "00-" + span.SpanContext().TraceID().String() + "-" + span.SpanContext().SpanID().String() + "-01"
	if got["admin.with_trace"] != want {
		t.Errorf("traceparent = %q, quería %q", got["admin.with_trace"], want)
	}
	if got["admin.without_trace"] != "" {
		t.Errorf("sin traza debería venir vacío: %q", got["admin.without_trace"])
	}
}

type traceStore struct{ events []outbox.Event }

func (s *traceStore) Dispatch(ctx context.Context, _ int, publish func(context.Context, outbox.Event) error) (int, error) {
	for _, e := range s.events {
		if err := publish(ctx, e); err != nil {
			return 0, err
		}
	}
	return len(s.events), nil
}

type tracePublisher struct{ seen chan trace.SpanContext }

func (p tracePublisher) Publish(ctx context.Context, _ string, _ any) error {
	p.seen <- trace.SpanContextFromContext(ctx)
	return nil
}

func TestDrainPublishesInsideTheTraceOfTheEvent(t *testing.T) {
	spans := tracing(t)
	store := &traceStore{events: []outbox.Event{{
		ID: "1", RoutingKey: "admin.traced", Payload: []byte(`{}`),
		Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	}}}
	publisher := tracePublisher{seen: make(chan trace.SpanContext, 1)}

	outbox.NewPoller(store, publisher, time.Second).Drain(context.Background())

	got := <-publisher.seen
	if got.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("el publicador debería continuar la traza del evento: %s", got.TraceID())
	}
	span := spans.Ended()[0]
	if span.Name() != "outbox publish admin.traced" || span.SpanKind() != trace.SpanKindProducer || span.Parent().SpanID().String() != "00f067aa0ba902b7" {
		t.Errorf("span inesperado: %s %v %v", span.Name(), span.SpanKind(), span.Parent())
	}
}

func TestDrainPublishesEventsWithoutATrace(t *testing.T) {
	tracing(t)
	store := &traceStore{events: []outbox.Event{{ID: "1", RoutingKey: "admin.untraced", Payload: []byte(`{}`)}}}
	publisher := tracePublisher{seen: make(chan trace.SpanContext, 1)}

	outbox.NewPoller(store, publisher, time.Second).Drain(context.Background())

	if !(<-publisher.seen).IsValid() {
		t.Error("el publicador siempre recibe un span de publicación, aunque el evento no traiga traza")
	}
}
