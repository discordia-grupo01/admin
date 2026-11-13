// Command voicedlq inspecciona y, de forma explícita, replaya las DLQ de voice.
//
//	VOICE_DLQ_QUEUES=voice.signaling.instance.replica,voice.signaling.instance.suspensions \
//	  go run ./cmd/voicedlq
//	VOICE_DLQ_QUEUES=voice.signaling.instance.replica go run ./cmd/voicedlq --replay --limit=100
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/discordia-grupo01/admin/internal/events/voicedlq"
	amqp "github.com/rabbitmq/amqp091-go"
)

const dialTimeout = 3 * time.Second

type amqpBroker struct {
	conn *amqp.Connection
	ch   *amqp.Channel
}

func (b *amqpBroker) QueueInspect(queue string) (amqp.Queue, error) {
	return b.ch.QueueInspect(queue)
}

func (b *amqpBroker) Get(queue string, autoAck bool) (amqp.Delivery, bool, error) {
	return b.ch.Get(queue, autoAck)
}

func (b *amqpBroker) PublishWithDeferredConfirmWithContext(ctx context.Context, exchange, key string, mandatory, immediate bool, msg amqp.Publishing) (*amqp.DeferredConfirmation, error) {
	return b.ch.PublishWithDeferredConfirmWithContext(ctx, exchange, key, mandatory, immediate, msg)
}

func (b *amqpBroker) Ack(tag uint64, multiple bool) error {
	return b.ch.Ack(tag, multiple)
}

func main() {
	fs := flag.NewFlagSet("voicedlq", flag.ExitOnError)
	replay := fs.Bool("replay", false, "republica mensajes a su exchange y routing key originales")
	limit := fs.Int("limit", 1, "máximo de mensajes a replayar por cola")
	threshold := fs.Int("threshold", 0, "cantidad máxima permitida; supera el umbral y termina con código 2")
	fs.Parse(os.Args[1:])

	queues, err := voicedlq.QueueNames(os.Getenv("VOICE_DLQ_QUEUES"))
	if err != nil {
		fail(err)
	}
	if *limit < 1 || *threshold < 0 {
		fail(errors.New("--limit debe ser mayor a cero y --threshold no puede ser negativo"))
	}
	url := strings.TrimSpace(os.Getenv("RABBITMQ_URL"))
	if url == "" {
		fail(errors.New("RABBITMQ_URL es obligatorio"))
	}

	conn, err := amqp.DialConfig(url, amqp.Config{Dial: amqp.DefaultDial(dialTimeout)})
	if err != nil {
		fail(fmt.Errorf("conectar a RabbitMQ: %w", err))
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		fail(fmt.Errorf("abrir canal RabbitMQ: %w", err))
	}
	defer ch.Close()
	if err := ch.Confirm(false); err != nil {
		fail(fmt.Errorf("activar confirmaciones RabbitMQ: %w", err))
	}

	broker := &amqpBroker{conn: conn, ch: ch}
	ctx := context.Background()
	alert := false
	for _, queue := range queues {
		depth, err := voicedlq.Depth(broker, queue)
		if err != nil {
			fail(err)
		}
		fmt.Printf("queue=%s%s messages=%d threshold=%d\n", queue, voicedlq.DeadLetterSuffix, depth, *threshold)
		if depth > *threshold {
			alert = true
		}
		if *replay && depth > 0 {
			count, err := voicedlq.Replay(ctx, broker, queue, *limit)
			if err != nil {
				fail(err)
			}
			fmt.Printf("queue=%s%s replayed=%d\n", queue, voicedlq.DeadLetterSuffix, count)
		}
	}
	if alert {
		os.Exit(2)
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "voicedlq: %v\n", err)
	os.Exit(1)
}
