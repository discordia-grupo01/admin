package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/discordia-grupo01/admin/internal/changes"

	"github.com/discordia-grupo01/admin/internal/audit"
	"github.com/discordia-grupo01/admin/internal/config"
	"github.com/discordia-grupo01/admin/internal/events"
	"github.com/discordia-grupo01/admin/internal/handler"
	"github.com/discordia-grupo01/admin/internal/logging"
	"github.com/discordia-grupo01/admin/internal/outbox"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/admin/internal/service"
	"github.com/discordia-grupo01/admin/internal/telemetry"
	"github.com/gin-gonic/gin"
)

const shutdownTimeout = 10 * time.Second

// @title        Discordia - Admin
// @version      1.0.0
// @description  API del backoffice de Discordia (épica Administración). Todos los endpoints `/v1/admin/*` requieren `Authorization: Bearer <JWT>` de un usuario que sea staff de la plataforma.
// @description
// @description  **Consistencia eventual**: suspender o reactivar (un usuario o un servidor) cambia el estado en admin al instante y publica un evento; los demás servicios lo aplican a los pocos instantes. Lo mismo al aprobar o rechazar una solicitud de recuperación de cuenta: identify desactiva el 2FA (o solo avisa al usuario) cuando procesa el evento.
// @description
// @description  **Expediente (#81)**: admin lo arma solo con datos locales, que le llegan por eventos (réplicas), así que puede ir unos segundos por detrás de los servicios de origen. Por minimización de datos nunca incluye el contenido de un mensaje, quién hizo un reporte ni quién aplicó una acción de moderación. La actividad de mensajes se conserva 90 días y empieza a registrarse desde el despliegue de esta versión; por eso un reporte de un mensaje anterior o más viejo que la retención no se puede atribuir a su autor (`reported_user_id: null`).
// @BasePath     /
// @securityDefinitions.apikey  BearerAuth
// @in                          header
// @name                        Authorization
// @description                 Escribir "Bearer " seguido del JWT. El usuario del token tiene que ser staff de la plataforma.
func main() {
	logging.MustSetup("admin")
	defer logging.Flush()

	if err := run(); err != nil {
		logging.Fatalf("admin: %v", err)
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

	shutdownTelemetry, err := telemetry.Setup(ctx, "admin")
	if err != nil {
		return err
	}
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = shutdownTelemetry(flushCtx)
	}()

	db, err := repository.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer db.Close()
	logging.Infof("conexión exitosa a PostgreSQL")

	staff := service.NewStaffService(repository.NewStaffRepository(db))
	if err := staff.SeedWithEvents(ctx, cfg.StaffUserIDs, cfg.AdminEventsHMACSecret); err != nil {
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

	changePublisher := changes.NewPublisher(changes.NewOutboxSink(db), repository.NewStaffAudience(db), 3*time.Second)

	userService := service.NewUserService(repository.NewUserRepository(db))
	users := handler.NewUserHandler(userService)

	// Las solicitudes de recuperación de cuenta llegan de identify por la misma
	// cola que los usuarios; aprobar o rechazar deja el evento firmado en el
	// outbox para que identify lo aplique.
	recoveryRepo := repository.NewRecoveryRequestRepository(db)
	recoveryService := service.NewRecoveryService(recoveryRepo, cfg.AdminEventsHMACSecret)
	recoveryRequests := handler.NewRecoveryHandler(recoveryService)

	// El consumer alimenta la réplica de usuarios con los eventos de identify.
	userConsumer := events.NewConsumer(cfg.RabbitMQURL, events.QueueUsers, events.UserBindings,
		changes.Notifying(events.NewUserEventsHandler(userService).WithRecoveryRequests(recoveryService), changePublisher), audit.NewPostgresRecorder(db))
	background.Add(1)
	go func() {
		defer background.Done()
		userConsumer.Start(ctx)
	}()

	// Y otro, la de servidores con los de servers.
	serverService := service.NewServerService(repository.NewServerRepository(db))
	serverConsumer := events.NewConsumer(cfg.RabbitMQURL, events.QueueServers, events.ServerBindings,
		changes.Notifying(events.NewServerEventsHandler(serverService).WithDossier(service.NewDossierSyncService(repository.NewDossierSyncRepository(db))), changePublisher),
		audit.NewPostgresRecorder(db))
	background.Add(1)
	go func() {
		defer background.Done()
		serverConsumer.Start(ctx)
	}()

	// Y un tercero registra la actividad de mensajes del expediente. Sin cuerpo
	// en la auditoría: el evento trae un extracto del texto, que admin no guarda.
	activityConsumer := events.NewConsumer(cfg.RabbitMQURL, events.QueueActivity, events.ActivityBindings,
		changes.Notifying(events.NewActivityEventsHandler(service.NewActivityService(repository.NewActivityRepository(db))), changePublisher),
		audit.WithoutBody(audit.NewPostgresRecorder(db)))
	background.Add(1)
	go func() {
		defer background.Done()
		activityConsumer.Start(ctx)
	}()

	// Una vez por hora borra la actividad que superó la retención.
	activityPruner := service.NewActivityPruner(repository.NewActivityRepository(db), cfg.ActivityRetentionDays, time.Hour)
	background.Add(1)
	go func() {
		defer background.Done()
		activityPruner.Start(ctx)
	}()

	// Suspender cambia el estado y deja el evento firmado en el outbox, que el
	// poller de arriba publica.
	suspensions := handler.NewSuspensionHandler(
		service.NewSuspensionService(repository.NewSuspensionRepository(db), cfg.AdminEventsHMACSecret))

	// Eliminar un servidor registra el pedido y deja el evento firmado en el
	// outbox; servers hace el borrado y el servidor sale del listado cuando lo
	// confirma con servers.server_deleted.
	serverModeration := handler.NewServerModerationHandler(
		service.NewServerModerationService(repository.NewServerModerationRepository(db), cfg.AdminEventsHMACSecret))

	// El expediente de investigación se arma solo con las réplicas locales.
	dossier := handler.NewDossierHandler(service.NewDossierService(repository.NewDossierRepository(db)).WithRecoveryRequests(recoveryRepo))

	router := handler.NewRouter(handler.Deps{
		JWTSecret: []byte(cfg.JWTSecret),
		Staff:     staff,
		Changes:   changePublisher,
		Health: handler.NewHealthHandler(
			handler.Check{Name: "postgres", Fn: db.PingContext},
			handler.Check{Name: "rabbitmq", Fn: func(context.Context) error { return publisher.Ping() }},
		),
	}, users.Register, handler.NewServerHandler(serverService).Register, suspensions.Register, serverModeration.Register, dossier.Register, recoveryRequests.Register)

	server := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		logging.Infof("escuchando en %s", cfg.Addr())
		serverErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		stop()
		background.Wait()
		return err
	case <-ctx.Done():
		logging.Infof("señal de apagado recibida, cerrando…")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logging.Errorf("apagado del servidor HTTP: %v", err)
	}
	background.Wait()
	return nil
}
