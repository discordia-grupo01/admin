// Command republishsuspensions vuelve a publicar un admin.user_suspended por cada
// usuario suspendido, con su versión vigente.
//
//	go run ./cmd/republishsuspensions
//	go run ./cmd/republishsuspensions --dry-run
//
// RabbitMQ no guarda los eventos publicados antes de que exista la cola de un
// consumidor. Un servicio que se despliega después de que ya había suspendidos
// (por ejemplo, el primer deploy de messaging o de voz) no se enteraría de
// ellos: este comando lo pone al día. Es seguro repetirlo, porque los
// consumidores ignoran los eventos cuya version ya aplicaron.
//
// Usa DATABASE_*, RABBITMQ_URL y ADMIN_EVENTS_HMAC_SECRET (los mismos que el
// servicio); no necesita JWT_SECRET. Publica directo al broker, sin pasar por el
// outbox, así que no depende de que la API esté corriendo y falla a la vista si
// RabbitMQ no responde.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/discordia-grupo01/admin/internal/config"
	"github.com/discordia-grupo01/admin/internal/events"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/admin/internal/service"
)

// publisher es lo que el comando necesita del broker.
type publisher interface {
	service.EventPublisher
	Close()
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	newPublisher := func(url string) publisher { return events.NewAMQPPublisher(url) }
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout, newPublisher); err != nil {
		fmt.Fprintf(os.Stderr, "republishsuspensions: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, out io.Writer, newPublisher func(url string) publisher) error {
	fs := flag.NewFlagSet("republishsuspensions", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dryRun := fs.Bool("dry-run", false, "solo cuenta las suspensiones vigentes, sin publicar nada")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("argumentos inválidos: %w (uso: republishsuspensions [--dry-run])", err)
	}

	dbConfig, err := config.LoadDatabase(getenv)
	if err != nil {
		return err
	}
	rabbitURL := strings.TrimSpace(getenv("RABBITMQ_URL"))
	secret := getenv("ADMIN_EVENTS_HMAC_SECRET")
	var missing []string
	if rabbitURL == "" {
		missing = append(missing, "RABBITMQ_URL")
	}
	if secret == "" {
		missing = append(missing, "ADMIN_EVENTS_HMAC_SECRET")
	}
	if len(missing) > 0 {
		return fmt.Errorf("faltan variables de entorno obligatorias: %s", strings.Join(missing, ", "))
	}

	db, err := repository.Open(ctx, dbConfig)
	if err != nil {
		return err
	}
	defer db.Close()

	svc := service.NewSuspensionService(repository.NewSuspensionRepository(db), secret)

	if *dryRun {
		n, err := svc.RepublishSuspensions(ctx, countingPublisher{})
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "[dry-run] hay %d suspensión(es) vigente(s); no se publicó nada\n", n)
		return nil
	}

	pub := newPublisher(rabbitURL)
	defer pub.Close()
	n, err := svc.RepublishSuspensions(ctx, pub)
	if err != nil {
		var already string
		if n > 0 {
			already = fmt.Sprintf(" (se alcanzaron a publicar %d)", n)
		}
		return errors.Join(fmt.Errorf("no se pudieron re-publicar todas las suspensiones%s", already), err)
	}
	fmt.Fprintf(out, "%d suspensión(es) re-publicada(s)\n", n)
	return nil
}

// countingPublisher no publica nada: sirve para contar en --dry-run.
type countingPublisher struct{}

func (countingPublisher) Publish(context.Context, string, any) error { return nil }
