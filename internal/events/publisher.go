// Package events conecta admin con RabbitMQ: un publisher para el outbox y un
// consumer genérico con ack manual, reconexión y descarte de mensajes venenosos.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/discordia-grupo01/events-contract/broker"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/discordia-grupo01/admin/internal/telemetry"
)

// dialTimeout acota cuánto puede tardar un intento de conexión, para que un
// broker que no responde no trabe el tick del poller del outbox.
const dialTimeout = 3 * time.Second

// PublisherConnectionName se muestra en la UI de management de RabbitMQ para
// identificar la conexión del publisher.
const PublisherConnectionName = "discordia-admin-events-publisher"

// AMQPPublisher publica en el exchange discordia.events. Se conecta de forma
// perezosa y, si un publish falla, reconecta y reintenta una vez; si vuelve a
// fallar devuelve el error, para que el outbox deje el evento pendiente.
type AMQPPublisher struct {
	url string

	mu   sync.Mutex
	conn *amqp.Connection
	ch   *amqp.Channel
}

func NewAMQPPublisher(url string) *AMQPPublisher {
	return &AMQPPublisher{url: url}
}

func (p *AMQPPublisher) Publish(ctx context.Context, routingKey string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("serializar el payload: %w", err)
	}
	msg := amqp.Publishing{
		ContentType:  "application/json",
		Body:         body,
		DeliveryMode: amqp.Persistent,
		Headers:      telemetry.InjectAMQP(ctx, nil),
	}

	ch, err := p.channel()
	if err != nil {
		return fmt.Errorf("RabbitMQ inaccesible: %w", err)
	}
	if err := publishConfirmed(ctx, ch, broker.ExchangeName, routingKey, msg); err != nil {
		p.reset()
		ch, err := p.channel()
		if err != nil {
			return fmt.Errorf("reconectar tras un publish fallido: %w", err)
		}
		if err := publishConfirmed(ctx, ch, broker.ExchangeName, routingKey, msg); err != nil {
			return fmt.Errorf("el publish volvió a fallar tras reconectar: %w", err)
		}
	}
	return nil
}

// Ping verifica que se pueda hablar con el broker (para /readyz).
func (p *AMQPPublisher) Ping() error {
	_, err := p.channel()
	return err
}

// Close cierra la conexión (para el apagado ordenado).
func (p *AMQPPublisher) Close() {
	p.reset()
}

// channel devuelve el canal abierto, conectando si hace falta (o si la
// conexión se cerró del lado del broker).
func (p *AMQPPublisher) channel() (*amqp.Channel, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ch != nil && !p.ch.IsClosed() {
		return p.ch, nil
	}
	p.closeLocked()

	props := amqp.NewConnectionProperties()
	props.SetClientConnectionName(PublisherConnectionName)
	conn, err := amqp.DialConfig(p.url, amqp.Config{Dial: amqp.DefaultDial(dialTimeout), Properties: props})
	if err != nil {
		return nil, err
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := ch.ExchangeDeclare(broker.ExchangeName, broker.ExchangeType, true, false, false, false, nil); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, err
	}
	if err := ch.Confirm(false); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, err
	}
	p.conn, p.ch = conn, ch
	return ch, nil
}

func (p *AMQPPublisher) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeLocked()
}

func (p *AMQPPublisher) closeLocked() {
	if p.ch != nil {
		_ = p.ch.Close()
	}
	if p.conn != nil {
		_ = p.conn.Close()
	}
	p.conn, p.ch = nil, nil
}

// confirmTimeout acota cuánto se espera la confirmación del broker.
var confirmTimeout = 5 * time.Second

// publishConfirmed publica y espera el ack del broker: sin él, un nil solo
// significa que el mensaje salió por el socket, no que RabbitMQ lo guardó, y
// el outbox marcaría como publicado un evento que se pudo perder.
func publishConfirmed(ctx context.Context, ch *amqp.Channel, exchange, routingKey string, msg amqp.Publishing) error {
	confirm, err := ch.PublishWithDeferredConfirmWithContext(ctx, exchange, routingKey, false, false, msg)
	if err != nil {
		return err
	}
	return waitConfirm(ctx, confirm)
}

// deferredConfirm es lo que usamos de *DeferredConfirmation; existe para poder
// probar la espera sin un RabbitMQ real.
type deferredConfirm interface {
	WaitContext(ctx context.Context) (bool, error)
}

func waitConfirm(ctx context.Context, confirm deferredConfirm) error {
	ctx, cancel := context.WithTimeout(ctx, confirmTimeout)
	defer cancel()
	acked, err := confirm.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("esperando la confirmación del broker: %w", err)
	}
	if !acked {
		return errors.New("el broker rechazó el mensaje")
	}
	return nil
}
