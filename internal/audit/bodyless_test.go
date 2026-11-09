package audit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/discordia-grupo01/admin/internal/audit"
)

type spyRecorder struct{ entries []audit.Entry }

func (s *spyRecorder) Record(_ context.Context, e audit.Entry) { s.entries = append(s.entries, e) }

func TestWithoutBodyDropsTheBodyAndTheProcessedEntries(t *testing.T) {
	spy := &spyRecorder{}
	rec := audit.WithoutBody(spy)
	ctx := context.Background()
	body := []byte(`{"content_preview":"texto privado"}`)

	rec.Record(ctx, audit.Entry{EventID: "e1", RoutingKey: "k", RawBody: body, Status: audit.StatusProcessed})
	rec.Record(ctx, audit.Entry{EventID: "e2", RoutingKey: "k", RawBody: body, Status: audit.StatusPending, Err: errors.New("db caída")})
	rec.Record(ctx, audit.Entry{EventID: "e3", RoutingKey: "k", RawBody: body, Status: audit.StatusDiscardedInvalid, Err: errors.New("x")})

	if len(spy.entries) != 2 {
		t.Fatalf("entradas = %+v, los procesados no se registran", spy.entries)
	}
	for _, e := range spy.entries {
		if e.RawBody != nil {
			t.Errorf("%s: el cuerpo no debió pasar: %q", e.EventID, e.RawBody)
		}
	}
	if spy.entries[0].Status != audit.StatusPending || spy.entries[0].Err == nil || spy.entries[1].Status != audit.StatusDiscardedInvalid {
		t.Errorf("tienen que conservar estado y error: %+v", spy.entries)
	}
}
