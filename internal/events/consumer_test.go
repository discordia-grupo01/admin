package events

import (
	"context"
	"errors"
	"testing"

	"github.com/discordia-grupo01/admin/internal/audit"
)

type handlerFunc func(ctx context.Context, routingKey string, body []byte) error

func (f handlerFunc) Handle(ctx context.Context, routingKey string, body []byte) error {
	return f(ctx, routingKey, body)
}

type recorderSpy struct{ entries []audit.Entry }

func (r *recorderSpy) Record(_ context.Context, e audit.Entry) { r.entries = append(r.entries, e) }

const body = `{"event_id":"evt-1","occurred_at":"2026-11-01T10:00:00Z","user_id":"u1"}`

func TestProcessSuccessAcks(t *testing.T) {
	rec := &recorderSpy{}
	requeue := Process(context.Background(), handlerFunc(func(context.Context, string, []byte) error { return nil }), rec, "users.profile_updated", []byte(body))

	if requeue {
		t.Error("un evento procesado no se reencola")
	}
	if len(rec.entries) != 1 || rec.entries[0].Status != audit.StatusProcessed || rec.entries[0].EventID != "evt-1" {
		t.Errorf("auditoría: %+v", rec.entries)
	}
}

func TestProcessTransientErrorRequeues(t *testing.T) {
	rec := &recorderSpy{}
	boom := errors.New("db caída")
	requeue := Process(context.Background(), handlerFunc(func(context.Context, string, []byte) error { return boom }), rec, "k", []byte(body))

	if !requeue {
		t.Error("una falla transitoria debe reencolarse")
	}
	if len(rec.entries) != 1 || rec.entries[0].Status != audit.StatusPending || !errors.Is(rec.entries[0].Err, boom) {
		t.Errorf("auditoría: %+v", rec.entries)
	}
}

func TestProcessDiscardDoesNotRequeue(t *testing.T) {
	rec := &recorderSpy{}
	h := handlerFunc(func(context.Context, string, []byte) error {
		return Discard(audit.StatusDiscardedInvalidSignature, errors.New("firma inválida"))
	})
	requeue := Process(context.Background(), h, rec, "admin.user_suspended", []byte(body))

	if requeue {
		t.Error("un descarte permanente no debe reencolarse: sería un mensaje venenoso")
	}
	if len(rec.entries) != 1 || rec.entries[0].Status != audit.StatusDiscardedInvalidSignature {
		t.Errorf("auditoría: %+v", rec.entries)
	}
}

func TestProcessDiscardWrappedStillDiscards(t *testing.T) {
	rec := &recorderSpy{}
	h := handlerFunc(func(context.Context, string, []byte) error {
		return errors.Join(errors.New("contexto"), Discard(audit.StatusDiscardedMalformed, errors.New("json roto")))
	})
	if Process(context.Background(), h, rec, "k", []byte(body)) {
		t.Error("un DiscardError envuelto también debe descartarse")
	}
}

func TestProcessBodyWithoutJSONStillRecords(t *testing.T) {
	rec := &recorderSpy{}
	h := handlerFunc(func(context.Context, string, []byte) error {
		return Discard(audit.StatusDiscardedMalformed, errors.New("no es JSON"))
	})
	requeue := Process(context.Background(), h, rec, "k", []byte("esto no es json"))

	if requeue || len(rec.entries) != 1 || rec.entries[0].EventID != "" || rec.entries[0].Status != audit.StatusDiscardedMalformed {
		t.Fatalf("requeue=%v auditoría=%+v", requeue, rec.entries)
	}
}

func TestDiscardErrorMessageAndUnwrap(t *testing.T) {
	inner := errors.New("causa")
	err := Discard(audit.StatusDiscardedInvalid, inner)
	if !errors.Is(err, inner) {
		t.Error("Unwrap debe exponer la causa")
	}
	if err.Error() == "" {
		t.Error("el mensaje no debe estar vacío")
	}
}
