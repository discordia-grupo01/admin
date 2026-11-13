package voicedlq

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	DeadLetterSuffix = ".dlq"
	DefaultExchange  = "discordia.events"
)

type Broker interface {
	QueueInspect(queue string) (amqp.Queue, error)
	Get(queue string, autoAck bool) (amqp.Delivery, bool, error)
	PublishWithDeferredConfirmWithContext(ctx context.Context, exchange, key string, mandatory, immediate bool, msg amqp.Publishing) (*amqp.DeferredConfirmation, error)
	Ack(tag uint64, multiple bool) error
}

func QueueNames(raw string) ([]string, error) {
	var queues []string
	seen := map[string]struct{}{}
	for _, value := range strings.Split(raw, ",") {
		queue := strings.TrimSpace(value)
		if queue == "" {
			continue
		}
		if !strings.HasPrefix(queue, "voice.") {
			return nil, fmt.Errorf("la cola %q no pertenece a voice", queue)
		}
		if _, ok := seen[queue]; ok {
			continue
		}
		seen[queue] = struct{}{}
		queues = append(queues, queue)
	}
	if len(queues) == 0 {
		return nil, errors.New("VOICE_DLQ_QUEUES debe contener al menos una cola voice.*")
	}
	return queues, nil
}

func Depth(broker Broker, queue string) (int, error) {
	info, err := broker.QueueInspect(queue + DeadLetterSuffix)
	if err != nil {
		return 0, fmt.Errorf("inspeccionar %s%s: %w", queue, DeadLetterSuffix, err)
	}
	return info.Messages, nil
}

func Replay(ctx context.Context, broker Broker, queue string, limit int) (int, error) {
	if limit < 1 {
		return 0, errors.New("el límite de replay debe ser mayor a cero")
	}

	replayed := 0
	for replayed < limit {
		delivery, ok, err := broker.Get(queue+DeadLetterSuffix, false)
		if err != nil {
			return replayed, fmt.Errorf("leer %s%s: %w", queue, DeadLetterSuffix, err)
		}
		if !ok {
			return replayed, nil
		}

		exchange := headerString(delivery.Headers, "x-original-exchange")
		if exchange == "" {
			exchange = DefaultExchange
		}
		routingKey := headerString(delivery.Headers, "x-original-routing-key")
		if routingKey == "" {
			return replayed, fmt.Errorf("mensaje %d de %s%s no tiene x-original-routing-key", delivery.DeliveryTag, queue, DeadLetterSuffix)
		}

		confirmation, err := broker.PublishWithDeferredConfirmWithContext(
			ctx, exchange, routingKey, false, false,
			amqp.Publishing{
				ContentType:  delivery.ContentType,
				DeliveryMode: amqp.Persistent,
				MessageId:    delivery.MessageId,
				Body:         delivery.Body,
			},
		)
		if err != nil {
			return replayed, fmt.Errorf("republicar mensaje %d: %w", delivery.DeliveryTag, err)
		}
		confirmed, err := confirmation.WaitContext(ctx)
		if err != nil {
			return replayed, fmt.Errorf("confirmar mensaje %d: %w", delivery.DeliveryTag, err)
		}
		if !confirmed {
			return replayed, fmt.Errorf("RabbitMQ no confirmó el mensaje %d", delivery.DeliveryTag)
		}
		if err := broker.Ack(delivery.DeliveryTag, false); err != nil {
			return replayed, fmt.Errorf("confirmar consumo del mensaje %d: %w", delivery.DeliveryTag, err)
		}
		replayed++
	}
	return replayed, nil
}

func headerString(headers amqp.Table, key string) string {
	value, ok := headers[key]
	if !ok {
		return ""
	}
	switch value := value.(type) {
	case string:
		return value
	case []byte:
		return string(value)
	case int:
		return strconv.Itoa(value)
	default:
		return fmt.Sprint(value)
	}
}
