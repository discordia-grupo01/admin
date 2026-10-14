package outbox

import (
	"context"
	"encoding/json"
	"log"
	"time"
)

const batchSize = 20

// Publisher publica un evento y devuelve error si no llegó al broker, para
// que el evento quede pendiente y se reintente.
type Publisher interface {
	Publish(ctx context.Context, routingKey string, payload any) error
}

type Poller struct {
	store     Store
	publisher Publisher
	interval  time.Duration
}

func NewPoller(store Store, publisher Publisher, interval time.Duration) *Poller {
	return &Poller{store: store, publisher: publisher, interval: interval}
}

// Start publica los eventos pendientes cada interval hasta que se cancele ctx.
func (p *Poller) Start(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.Drain(ctx)
		}
	}
}

// Drain publica un lote de eventos pendientes. Un error del broker deja el
// evento pendiente (con attempts y last_error) y se reintenta en el próximo
// tick; un error de base solo se loguea.
func (p *Poller) Drain(ctx context.Context) {
	n, err := p.store.Dispatch(ctx, batchSize, func(ctx context.Context, e Event) error {
		return p.publisher.Publish(ctx, e.RoutingKey, json.RawMessage(e.Payload))
	})
	if err != nil {
		log.Printf("outbox: falló el tick del poller: %v", err)
		return
	}
	if n > 0 {
		log.Printf("outbox: %d evento(s) publicados", n)
	}
}
