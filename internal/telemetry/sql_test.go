package telemetry_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/discordia-grupo01/admin/internal/telemetry"
)

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return fakeConn{}, nil }

type fakeConn struct{}

func (fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not supported") }
func (fakeConn) Close() error                        { return nil }
func (fakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("not supported") }

func (fakeConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	if strings.Contains(query, "FAIL") {
		return nil, errors.New("duplicate key persona@example.com")
	}
	return driver.ResultNoRows, nil
}

func init() { sql.Register("fake-telemetry", fakeDriver{}) }

func TestOpenSQLRecordsQueriesInsideATrace(t *testing.T) {
	spans := recorder(t)
	db, err := telemetry.OpenSQL("fake-telemetry", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, parent := otel.Tracer("test").Start(context.Background(), "request")

	if _, err := db.ExecContext(ctx, "  insert into users (email) values ($1)", "persona@example.com"); err != nil {
		t.Fatal(err)
	}
	parent.End()

	var query = spans.Ended()[0]
	for _, span := range spans.Ended() {
		if span.Name() == "db INSERT" {
			query = span
		}
	}
	if query.Name() != "db INSERT" || query.SpanKind() != trace.SpanKindClient {
		t.Fatalf("span inesperado: %s (%v)", query.Name(), query.SpanKind())
	}
	got := attributes(query)
	if got["db.system"] != "postgresql" || !strings.Contains(toString(got["db.statement"]), "values ($1)") {
		t.Errorf("atributos inesperados: %v", got)
	}
	for key, value := range got {
		if strings.Contains(toString(value), "persona@example.com") {
			t.Errorf("el argumento se coló en %s", key)
		}
	}
	if query.Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Error("la consulta debería colgar del pedido")
	}
}

func TestOpenSQLIgnoresQueriesOutsideATrace(t *testing.T) {
	spans := recorder(t)
	db, err := telemetry.OpenSQL("fake-telemetry", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.ExecContext(context.Background(), "SELECT 1"); err != nil {
		t.Fatal(err)
	}

	if len(spans.Ended()) != 0 {
		t.Errorf("no debería haber spans fuera de una traza: %d", len(spans.Ended()))
	}
}

func TestOpenSQLMarksFailedQueries(t *testing.T) {
	spans := recorder(t)
	db, err := telemetry.OpenSQL("fake-telemetry", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, parent := otel.Tracer("test").Start(context.Background(), "request")

	_, err = db.ExecContext(ctx, "UPDATE FAIL")
	parent.End()

	if err == nil {
		t.Fatal("la consulta debería fallar")
	}
	for _, span := range spans.Ended() {
		if span.Name() == "db UPDATE" && span.Status().Code == codes.Error {
			return
		}
	}
	t.Error("la consulta fallida debería marcar su span como error")
}

func TestOpenSQLFailsForAnUnknownDriver(t *testing.T) {
	recorder(t)

	if _, err := telemetry.OpenSQL("driver-que-no-existe", ""); err == nil {
		t.Error("un driver desconocido debería fallar")
	}
}
