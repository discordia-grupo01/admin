package service

import (
	"context"
	"fmt"
	"time"

	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/events-contract/admin"
	"github.com/discordia-grupo01/events-contract/envelope"
	"github.com/google/uuid"
)

// Errores de negocio de la moderación de servidores; el handler los traduce a HTTP.
var (
	ErrServerNotFound             = repository.ErrServerNotFound
	ErrDeleteConfirmationMismatch = repository.ErrDeleteConfirmationMismatch
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

// Delete registra el pedido de eliminación del servidor serverID en nombre del
// staff staffID y deja escrito en el outbox el evento
// admin.server_deletion_requested, firmado. No borra nada: servers ejecuta el
// borrado y avisa con servers.server_deleted, que es lo que saca al servidor del
// listado. Un id que no es un UUID no puede existir, así que se trata como
// servidor inexistente.
func (s *ServerModerationService) Delete(ctx context.Context, serverID, confirmName, staffID string) error {
	parsed, err := uuid.Parse(serverID)
	if err != nil {
		return ErrServerNotFound
	}
	serverID = parsed.String()

	// Postgres guarda microsegundos: se trunca acá para que la fila y el evento
	// muestren exactamente la misma hora.
	at := s.now().UTC().Truncate(time.Microsecond)
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
