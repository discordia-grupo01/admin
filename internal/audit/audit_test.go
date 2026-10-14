package audit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/discordia-grupo01/admin/internal/audit"
	"github.com/discordia-grupo01/admin/internal/testutil"
)

func TestPostgresRecorder(t *testing.T) {
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "consumed_events")
	rec := audit.NewPostgresRecorder(db)
	ctx := context.Background()

	// Un fallo transitorio queda pendiente y sin processed_at.
	rec.Record(ctx, audit.Entry{EventID: "e1", RoutingKey: "k", RawBody: []byte(`{}`), Status: audit.StatusPending, Err: errors.New("db caída")})
	var status string
	var attempts int
	var processed *string
	scan := func() {
		t.Helper()
		if err := db.QueryRow(`SELECT status, attempts, processed_at::text FROM consumed_events WHERE event_id = 'e1'`).Scan(&status, &attempts, &processed); err != nil {
			t.Fatal(err)
		}
	}
	scan()
	if status != string(audit.StatusPending) || attempts != 1 || processed != nil {
		t.Errorf("pendiente: status=%s attempts=%d processed=%v", status, attempts, processed)
	}

	// El reintento que sale bien actualiza la misma fila (sin duplicar) y suma un intento.
	rec.Record(ctx, audit.Entry{EventID: "e1", RoutingKey: "k", RawBody: []byte(`{}`), Status: audit.StatusProcessed})
	scan()
	if status != string(audit.StatusProcessed) || attempts != 2 || processed == nil {
		t.Errorf("procesado: status=%s attempts=%d processed=%v", status, attempts, processed)
	}
	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM consumed_events`).Scan(&rows); err != nil || rows != 1 {
		t.Errorf("filas=%d err=%v, quería 1", rows, err)
	}
}

func TestPostgresRecorderWithoutEventID(t *testing.T) {
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "consumed_events")
	rec := audit.NewPostgresRecorder(db)

	// Sin event_id no hay clave para deduplicar: cada entrega es una fila.
	for i := 0; i < 2; i++ {
		rec.Record(context.Background(), audit.Entry{RoutingKey: "k", RawBody: []byte("basura"), Status: audit.StatusDiscardedMalformed, Err: errors.New("no es JSON")})
	}
	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM consumed_events WHERE event_id IS NULL`).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("filas=%d err=%v, quería 2", rows, err)
	}
}
