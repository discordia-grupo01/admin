package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/discordia-grupo01/admin/internal/audit"
	"github.com/discordia-grupo01/admin/internal/config"
	"github.com/discordia-grupo01/admin/internal/events"
	"github.com/discordia-grupo01/admin/internal/handler"
	"github.com/discordia-grupo01/admin/internal/outbox"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/admin/internal/service"
	"github.com/gin-gonic/gin"
)

const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		log.Fatalf("admin: %v", err)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	if cfg.Environment == config.EnvProduction {
		gin.SetMode(gin.ReleaseMode)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := repository.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer db.Close()
	log.Println("conexión exitosa a PostgreSQL")

	staff := service.NewStaffService(repository.NewStaffRepository(db))
	if err := staff.Seed(ctx, cfg.StaffUserIDs); err != nil {
		return err
	}

	publisher := events.NewAMQPPublisher(cfg.RabbitMQURL)
	defer publisher.Close()

	// El poller publica los eventos del outbox. Se espera a que termine antes de
	// cerrar la base y el broker.
	var background sync.WaitGroup
	poller := outbox.NewPoller(outbox.NewPostgresStore(db), publisher, cfg.OutboxPollInterval)
	background.Add(1)
	go func() {
		defer background.Done()
		poller.Start(ctx)
	}()

	userService := service.NewUserService(repository.NewUserRepository(db))
	users := handler.NewUserHandler(userService)

	// El consumer alimenta la réplica de usuarios con los eventos de identify.
	userConsumer := events.NewConsumer(cfg.RabbitMQURL, events.QueueUsers, events.UserBindings,
		events.NewUserEventsHandler(userService), audit.NewPostgresRecorder(db))
	background.Add(1)
	go func() {
		defer background.Done()
		userConsumer.Start(ctx)
	}()

	// Y otro, la de servidores con los de servers.
	serverService := service.NewServerService(repository.NewServerRepository(db))
	serverConsumer := events.NewConsumer(cfg.RabbitMQURL, events.QueueServers, events.ServerBindings,
		events.NewServerEventsHandler(serverService), audit.NewPostgresRecorder(db))
	background.Add(1)
	go func() {
		defer background.Done()
		serverConsumer.Start(ctx)
	}()

	// Suspender cambia el estado y deja el evento firmado en el outbox, que el
	// poller de arriba publica.
	suspensions := handler.NewSuspensionHandler(
		service.NewSuspensionService(repository.NewSuspensionRepository(db), cfg.AdminEventsHMACSecret))

	router := handler.NewRouter(handler.Deps{
		JWTSecret: []byte(cfg.JWTSecret),
		Staff:     staff,
		Health: handler.NewHealthHandler(
			handler.Check{Name: "postgres", Fn: db.PingContext},
			handler.Check{Name: "rabbitmq", Fn: func(context.Context) error { return publisher.Ping() }},
		),
	}, users.Register, handler.NewServerHandler(serverService).Register, suspensions.Register)

	server := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Printf("escuchando en %s", cfg.Addr())
		serverErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		stop()
		background.Wait()
		return err
	case <-ctx.Done():
		log.Println("señal de apagado recibida, cerrando…")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("apagado del servidor HTTP: %v", err)
	}
	background.Wait()
	return nil
}
