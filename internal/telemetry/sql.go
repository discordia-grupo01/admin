package telemetry

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"

	"github.com/XSAM/otelsql"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func OpenSQL(driverName, dataSourceName string) (*sql.DB, error) {
	return otelsql.Open(driverName, dataSourceName,
		otelsql.WithAttributes(attribute.String("db.system", "postgresql")),
		otelsql.WithSpanNameFormatter(func(_ context.Context, method otelsql.Method, query string) string {
			if query == "" {
				return string(method)
			}
			return "db " + operationOf(query)
		}),
		otelsql.WithSpanOptions(otelsql.SpanOptions{
			DisableErrSkip:       true,
			OmitConnResetSession: true,
			OmitConnPrepare:      true,
			OmitRows:             true,
			OmitConnectorConnect: true,
			SpanFilter: func(ctx context.Context, _ otelsql.Method, _ string, _ []driver.NamedValue) bool {
				return trace.SpanContextFromContext(ctx).IsValid()
			},
		}),
	)
}

func operationOf(query string) string {
	fields := strings.Fields(query)
	if len(fields) == 0 {
		return "query"
	}
	return strings.ToUpper(fields[0])
}
