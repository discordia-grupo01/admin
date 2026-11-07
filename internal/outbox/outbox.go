// Package outbox implementa el outbox transaccional: un evento se inserta en
// outbox_events en la misma transacción que el cambio de negocio que lo
// origina, y un Poller lo publica después en RabbitMQ. Así un corte de
// RabbitMQ o un crash entre el commit y el publish nunca pierde el evento,
// solo lo demora. Mismo diseño que el outbox de identify y servers.
package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/discordia-grupo01/admin/internal/telemetry"
)

// Event es un evento pendiente de publicar.
type Event struct {
	ID          string
	RoutingKey  string
	Payload     []byte
	Traceparent string
}

// Insert guarda un evento pendiente dentro de tx. Si falla, el caller tiene
// que hacer rollback: nunca queda un cambio de negocio sin su evento.
func Insert(ctx context.Context, tx *sql.Tx, routingKey string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("outbox: serializar el payload de %q: %w", routingKey, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO outbox_events (id, routing_key, payload, traceparent)
		VALUES ($1, $2, $3, NULLIF($4, ''))`,
		uuid.NewString(), routingKey, body, telemetry.Traceparent(ctx),
	); err != nil {
		return fmt.Errorf("outbox: insertar el evento %q: %w", routingKey, err)
	}
	return nil
}
