// Command seedstaff da de alta como staff de la plataforma al usuario con el
// email indicado, buscándolo en la réplica de usuarios de admin.
//
//	go run ./cmd/seedstaff --email manu@example.com
//
// Es el mecanismo para otorgar el rol por fuera de la API (el rol de staff no se
// puede auto-asignar). Alternativa cuando se conoce el UUID: STAFF_USER_IDS.
// Usa la misma conexión a la base que el servicio (DATABASE_*), pero no necesita
// JWT_SECRET ni RabbitMQ. Es idempotente.
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
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/admin/internal/service"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "seedstaff: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, out io.Writer) error {
	fs := flag.NewFlagSet("seedstaff", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	email := fs.String("email", "", "email del usuario a dar de alta como staff")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("argumentos inválidos: %w (uso: seedstaff --email <email>)", err)
	}
	if strings.TrimSpace(*email) == "" {
		return errors.New("falta --email (uso: seedstaff --email <email>)")
	}

	dbConfig, err := config.LoadDatabase(getenv)
	if err != nil {
		return err
	}
	db, err := repository.Open(ctx, dbConfig)
	if err != nil {
		return err
	}
	defer db.Close()

	staff := service.NewStaffService(repository.NewStaffRepository(db))
	secret := strings.TrimSpace(getenv("ADMIN_EVENTS_HMAC_SECRET"))
	if secret == "" {
		return errors.New("falta ADMIN_EVENTS_HMAC_SECRET para publicar el cambio de staff")
	}
	id, err := staff.SeedByEmailWithEvents(ctx, service.NewUserService(repository.NewUserRepository(db)), *email, secret)
	if errors.Is(err, repository.ErrUserNotFound) {
		return fmt.Errorf("no hay ningún usuario con el email %q en la réplica de admin: tiene que haberse registrado en identify y su alta (users.account_registered) tiene que haber llegado; si solo conocés su UUID, usá STAFF_USER_IDS", *email)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s (%s) ahora es staff de la plataforma\n", strings.TrimSpace(*email), id)
	return nil
}
