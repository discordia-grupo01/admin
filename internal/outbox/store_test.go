package outbox_test

import (
	"context"
	"errors"
	"testing"

	"github.com/discordia-grupo01/admin/internal/outbox"
	"github.com/discordia-grupo01/admin/internal/testutil"
)

func TestPostgresStoreDispatch(t *testing.T) {
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "outbox_events")
	store := outbox.NewPostgresStore(db)
	ctx := context.Background()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"admin.first", "admin.second", "admin.third"} {
		if err := outbox.Insert(ctx, tx, key, map[string]string{"k": key}); err != nil {
			t.Fatalf("Insert %s: %v", key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// El broker falla en el segundo: el primero queda publicado, el resto pendiente.
	var seen []string
	n, err := store.Dispatch(ctx, 10, func(_ context.Context, e outbox.Event) error {
		seen = append(seen, e.RoutingKey)
		if e.RoutingKey == "admin.second" {
			return errors.New("broker caído")
		}
		return nil
	})
	if err != nil || n != 1 {
		t.Fatalf("Dispatch: n=%d err=%v, quería n=1", n, err)
	}
	if len(seen) != 2 || seen[0] != "admin.first" {
		t.Errorf("orden inesperado: %v", seen)
	}

	var attempts int
	var lastErr *string
	if err := db.QueryRow(`SELECT attempts, last_error FROM outbox_events WHERE routing_key = 'admin.second'`).Scan(&attempts, &lastErr); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || lastErr == nil || *lastErr != "broker caído" {
		t.Errorf("attempts=%d last_error=%v", attempts, lastErr)
	}

	// Con el broker de vuelta se publican los que faltan, sin repetir el primero.
	seen = nil
	n, err = store.Dispatch(ctx, 10, func(_ context.Context, e outbox.Event) error {
		seen = append(seen, e.RoutingKey)
		return nil
	})
	if err != nil || n != 2 || len(seen) != 2 || seen[0] != "admin.second" || seen[1] != "admin.third" {
		t.Fatalf("segundo Dispatch: n=%d err=%v seen=%v", n, err, seen)
	}

	// Ya no queda nada pendiente.
	n, err = store.Dispatch(ctx, 10, func(context.Context, outbox.Event) error { return errors.New("no debería llamarse") })
	if err != nil || n != 0 {
		t.Fatalf("tercer Dispatch: n=%d err=%v", n, err)
	}
}

func TestInsertRollsBackWithTransaction(t *testing.T) {
	db := testutil.OpenDB(t)
	testutil.Truncate(t, db, "outbox_events")
	ctx := context.Background()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := outbox.Insert(ctx, tx, "admin.rolled_back", map[string]string{}); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM outbox_events`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("un rollback no debe dejar eventos: count=%d err=%v", count, err)
	}
}

func TestInsertRejectsUnserializablePayload(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	if err := outbox.Insert(ctx, tx, "admin.bad", make(chan int)); err == nil {
		t.Fatal("esperaba error al serializar un canal")
	}
}
