package service

import (
	"context"
	"fmt"
	"time"

	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/events-contract/admin"
	"github.com/discordia-grupo01/events-contract/envelope"
	"github.com/google/uuid"
)

// Errores de negocio de la moderación de servidores; el handler los traduce a HTTP.
var (
	ErrServerNotFound             = repository.ErrServerNotFound
	ErrDeleteConfirmationMismatch = repository.ErrDeleteConfirmationMismatch
	ErrServerAlreadySuspended     = repository.ErrServerAlreadySuspended
	ErrServerNotSuspended         = repository.ErrServerNotSuspended
)

type ServerModerationService struct {
	repo       repository.ServerModerationRepository
	hmacSecret string
	now        func() time.Time
	newEventID func() string
}

// NewServerModerationService recibe el secreto con el que se firman los eventos admin.*.
func NewServerModerationService(repo repository.ServerModerationRepository, hmacSecret string) *ServerModerationService {
	return &ServerModerationService{
		repo:       repo,
		hmacSecret: hmacSecret,
		now:        time.Now,
		newEventID: uuid.NewString,
	}
}

// parseServerID normaliza el id. Uno que no es un UUID no puede existir, así que
// se trata como servidor inexistente.
func parseServerID(serverID string) (string, error) {
	parsed, err := uuid.Parse(serverID)
	if err != nil {
		return "", ErrServerNotFound
	}
	return parsed.String(), nil
}

// timestamp es la hora de la acción. Postgres guarda microsegundos: se trunca
// acá para que la fila y el evento muestren exactamente la misma.
func (s *ServerModerationService) timestamp() time.Time {
	return s.now().UTC().Truncate(time.Microsecond)
}

// Delete registra el pedido de eliminación del servidor serverID en nombre del
// staff staffID y deja escrito en el outbox el evento
// admin.server_deletion_requested, firmado. No borra nada: servers ejecuta el
// borrado y avisa con servers.server_deleted, que es lo que saca al servidor del
// listado.
func (s *ServerModerationService) Delete(ctx context.Context, serverID, confirmName, staffID string) error {
	serverID, err := parseServerID(serverID)
	if err != nil {
		return err
	}

	at := s.timestamp()
	err = s.repo.RequestDeletion(ctx, serverID, confirmName, staffID, at, func(version int64) (string, any) {
		e := admin.ServerDeletionRequested{
			Meta:     envelope.Meta{EventID: s.newEventID(), OccurredAt: at},
			ServerID: serverID,
			Version:  version,
		}
		e.Sign(s.hmacSecret)
		return admin.RoutingKeyServerDeletionRequested, e
	})
	if err != nil {
		return fmt.Errorf("pedir la eliminación del servidor: %w", err)
	}
	return nil
}

// Suspend suspende el servidor serverID en nombre del staff staffID y deja
// escrito en el outbox el evento admin.server_suspended, firmado. El contenido
// del servidor se conserva; servers, messaging y voz bloquean su uso al procesar
// el evento.
func (s *ServerModerationService) Suspend(ctx context.Context, serverID, staffID string) (*model.Server, error) {
	serverID, err := parseServerID(serverID)
	if err != nil {
		return nil, err
	}

	at := s.timestamp()
	srv, err := s.repo.Suspend(ctx, serverID, staffID, at, func(version int64) (string, any) {
		e := admin.ServerSuspended{
			Meta:     envelope.Meta{EventID: s.newEventID(), OccurredAt: at},
			ServerID: serverID,
			Version:  version,
		}
		e.Sign(s.hmacSecret)
		return admin.RoutingKeyServerSuspended, e
	})
	if err != nil {
		return nil, fmt.Errorf("suspender el servidor: %w", err)
	}
	return srv, nil
}

// Reactivate levanta la suspensión de serverID y deja escrito en el outbox el
// evento admin.server_reactivated, firmado. Su version es la siguiente a la de
// la suspensión, así que los consumidores lo aplican por encima de ella.
func (s *ServerModerationService) Reactivate(ctx context.Context, serverID string) (*model.Server, error) {
	serverID, err := parseServerID(serverID)
	if err != nil {
		return nil, err
	}

	at := s.timestamp()
	srv, err := s.repo.Reactivate(ctx, serverID, func(version int64) (string, any) {
		e := admin.ServerReactivated{
			Meta:     envelope.Meta{EventID: s.newEventID(), OccurredAt: at},
			ServerID: serverID,
			Version:  version,
		}
		e.Sign(s.hmacSecret)
		return admin.RoutingKeyServerReactivated, e
	})
	if err != nil {
		return nil, fmt.Errorf("reactivar el servidor: %w", err)
	}
	return srv, nil
}

// RepublishServerSuspensions vuelve a publicar un admin.server_suspended por
// cada servidor suspendido, con la version vigente, para que un consumidor nuevo
// (que no estaba cuando se suspendió) se ponga al día. Cada evento lleva un
// event_id nuevo y firma propia; occurred_at es la fecha real de la suspensión.
// Es seguro repetirlo: los consumidores ignoran lo que ya aplicaron por version.
// Corta en el primer error de publicación y devuelve cuántos alcanzó a publicar.
func (s *ServerModerationService) RepublishServerSuspensions(ctx context.Context, pub EventPublisher) (int, error) {
	suspended, err := s.repo.ListSuspended(ctx)
	if err != nil {
		return 0, fmt.Errorf("listar servidores suspendidos: %w", err)
	}
	for i, srv := range suspended {
		e := admin.ServerSuspended{
			Meta:     envelope.Meta{EventID: s.newEventID(), OccurredAt: srv.SuspendedAt.UTC()},
			ServerID: srv.ServerID,
			Version:  srv.Version,
		}
		e.Sign(s.hmacSecret)
		if err := pub.Publish(ctx, admin.RoutingKeyServerSuspended, e); err != nil {
			return i, fmt.Errorf("publicar la suspensión de %s: %w", srv.ServerID, err)
		}
	}
	return len(suspended), nil
}
