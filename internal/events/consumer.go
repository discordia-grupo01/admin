package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/discordia-grupo01/admin/internal/audit"
	"github.com/discordia-grupo01/events-contract/broker"
	"github.com/discordia-grupo01/events-contract/envelope"
	amqp "github.com/rabbitmq/amqp091-go"
)

// Handler procesa un evento. Devuelve nil si lo aplicó, un DiscardError si el
// mensaje es inválido para siempre (no tiene sentido reintentarlo) o cualquier
// otro error si la falla es transitoria (se reencola).
type Handler interface {
	Handle(ctx context.Context, routingKey string, body []byte) error
}

// DiscardError marca un fallo permanente: el mensaje se confirma (ack) y se
// registra en la auditoría con el Status indicado. Reencolarlo crearía un
// "mensaje venenoso" que se reintenta para siempre.
type DiscardError struct {
	Status audit.Status
	Err    error
}

func (e *DiscardError) Error() string { return fmt.Sprintf("%s: %v", e.Status, e.Err) }
func (e *DiscardError) Unwrap() error { return e.Err }

// Discard arma un DiscardError.
func Discard(status audit.Status, err error) error {
	return &DiscardError{Status: status, Err: err}
}

// Process aplica el handler a un mensaje, registra el resultado y dice si hay
// que reencolarlo (true) o confirmarlo (false). Está separado del loop de AMQP
// para poder probarlo sin un broker.
func Process(ctx context.Context, handler Handler, recorder audit.Recorder, routingKey string, body []byte) (requeue bool) {
	// event_id es solo para la auditoría; si el cuerpo no es JSON, lo decide el handler.
	var meta envelope.Meta
	_ = json.Unmarshal(body, &meta)

	err := handler.Handle(ctx, routingKey, body)
	if err == nil {
		log.Printf("events: procesado %q event_id=%s", routingKey, meta.EventID)
		recorder.Record(ctx, audit.Entry{EventID: meta.EventID, RoutingKey: routingKey, RawBody: body, Status: audit.StatusProcessed})
		return false
	}

	var discard *DiscardError
	if errors.As(err, &discard) {
		log.Printf("events: descartado %q event_id=%s (%s): %v", routingKey, meta.EventID, discard.Status, discard.Err)
		recorder.Record(ctx, audit.Entry{EventID: meta.EventID, RoutingKey: routingKey, RawBody: body, Status: discard.Status, Err: discard.Err})
		return false
	}

	log.Printf("events: fallo transitorio procesando %q event_id=%s, reintentando: %v", routingKey, meta.EventID, err)
	recorder.Record(ctx, audit.Entry{EventID: meta.EventID, RoutingKey: routingKey, RawBody: body, Status: audit.StatusPending, Err: err})
	return true
}

// Consumer lee de una cola durable propia, enlazada al exchange discordia.events
// con las routing keys indicadas, con ack manual (at-least-once).
type Consumer struct {
	url      string
	queue    string
	bindings []string
	handler  Handler
	recorder audit.Recorder
}

func NewConsumer(url, queue string, bindings []string, handler Handler, recorder audit.Recorder) *Consumer {
	return &Consumer{url: url, queue: queue, bindings: bindings, handler: handler, recorder: recorder}
}

// Start consume hasta que se cancele ctx, reconectando con backoff exponencial.
func (c *Consumer) Start(ctx context.Context) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for ctx.Err() == nil {
		if err := c.consumeOnce(ctx); err != nil {
			log.Printf("events: conexión a RabbitMQ interrumpida (cola %s), reintentando en %s: %v", c.queue, backoff, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		backoff = time.Second
	}
}

func (c *Consumer) consumeOnce(ctx context.Context) error {
	props := amqp.NewConnectionProperties()
	props.SetClientConnectionName("discordia-admin-" + c.queue)
	conn, err := amqp.DialConfig(c.url, amqp.Config{Dial: amqp.DefaultDial(dialTimeout), Properties: props})
	if err != nil {
		return err
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	if err := ch.ExchangeDeclare(broker.ExchangeName, broker.ExchangeType, true, false, false, false, nil); err != nil {
		return err
	}
	queue, err := ch.QueueDeclare(c.queue, true, false, false, false, nil)
	if err != nil {
		return err
	}
	for _, key := range c.bindings {
		if err := ch.QueueBind(queue.Name, key, broker.ExchangeName, false, nil); err != nil {
			return err
		}
	}

	// Ack manual: el mensaje solo se confirma después de procesarlo, para no
	// perder eventos ante una falla transitoria de la base.
	messages, err := ch.Consume(queue.Name, "", false, false, false, false, nil)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case delivery, ok := <-messages:
			if !ok {
				return errors.New("canal de consumo cerrado")
			}
			if Process(ctx, c.handler, c.recorder, delivery.RoutingKey, delivery.Body) {
				_ = delivery.Nack(false, true)
			} else {
				_ = delivery.Ack(false)
			}
		}
	}
}
