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

// Errores de negocio de la suspensión; el handler los traduce a HTTP.
var (
	ErrUserNotFound         = repository.ErrUserNotFound
	ErrCannotSuspendStaff   = repository.ErrTargetIsStaff
	ErrUserAlreadySuspended = repository.ErrAlreadySuspended
	ErrUserNotSuspended     = repository.ErrNotSuspended
)

type SuspensionService struct {
	repo       repository.SuspensionRepository
	hmacSecret string
	now        func() time.Time
	newEventID func() string
}

// NewSuspensionService recibe el secreto con el que se firman los eventos admin.*.
func NewSuspensionService(repo repository.SuspensionRepository, hmacSecret string) *SuspensionService {
	return &SuspensionService{
		repo:       repo,
		hmacSecret: hmacSecret,
		now:        time.Now,
		newEventID: uuid.NewString,
	}
}

// Suspend suspende al usuario userID en nombre del staff staffID y deja
// escrito en el outbox el evento admin.user_suspended, firmado. Un id que no es
// un UUID no puede existir, así que se trata como usuario inexistente.
func (s *SuspensionService) Suspend(ctx context.Context, userID, staffID string) (*model.User, error) {
	parsed, err := uuid.Parse(userID)
	if err != nil {
		return nil, ErrUserNotFound
	}
	userID = parsed.String()

	// Postgres guarda microsegundos: se trunca acá para que la respuesta, el
	// evento y el listado muestren exactamente la misma hora.
	at := s.now().UTC().Truncate(time.Microsecond)
	u, err := s.repo.Suspend(ctx, userID, staffID, at, func(version int64) (string, any) {
		e := admin.UserSuspended{
			Meta:    envelope.Meta{EventID: s.newEventID(), OccurredAt: at},
			UserID:  userID,
			Version: version,
		}
		e.Sign(s.hmacSecret)
		return admin.RoutingKeyUserSuspended, e
	})
	if err != nil {
		return nil, fmt.Errorf("suspender usuario: %w", err)
	}
	return u, nil
}

// Reactivate levanta la suspensión de userID y deja escrito en el outbox el
// evento admin.user_reactivated, firmado. Su version es la siguiente a la de la
// suspensión, así que los consumidores lo aplican por encima de ella.
func (s *SuspensionService) Reactivate(ctx context.Context, userID string) (*model.User, error) {
	parsed, err := uuid.Parse(userID)
	if err != nil {
		return nil, ErrUserNotFound
	}
	userID = parsed.String()

	at := s.now().UTC().Truncate(time.Microsecond)
	u, err := s.repo.Reactivate(ctx, userID, func(version int64) (string, any) {
		e := admin.UserReactivated{
			Meta:    envelope.Meta{EventID: s.newEventID(), OccurredAt: at},
			UserID:  userID,
			Version: version,
		}
		e.Sign(s.hmacSecret)
		return admin.RoutingKeyUserReactivated, e
	})
	if err != nil {
		return nil, fmt.Errorf("reactivar usuario: %w", err)
	}
	return u, nil
}

// EventPublisher publica un evento en el bus.
type EventPublisher interface {
	Publish(ctx context.Context, routingKey string, payload any) error
}

// RepublishSuspensions vuelve a publicar un admin.user_suspended por cada
// usuario suspendido, con la version vigente, para que un consumidor nuevo (que
// no estaba cuando se suspendió) se ponga al día. Cada evento lleva un event_id
// nuevo y firma propia; occurred_at es la fecha real de la suspensión. Es
// seguro repetirlo: los consumidores ignoran lo que ya aplicaron por version.
// Corta en el primer error de publicación y devuelve cuántos alcanzó a publicar.
func (s *SuspensionService) RepublishSuspensions(ctx context.Context, pub EventPublisher) (int, error) {
	suspended, err := s.repo.ListSuspended(ctx)
	if err != nil {
		return 0, fmt.Errorf("listar suspendidos: %w", err)
	}
	for i, u := range suspended {
		e := admin.UserSuspended{
			Meta:    envelope.Meta{EventID: s.newEventID(), OccurredAt: u.SuspendedAt.UTC()},
			UserID:  u.UserID,
			Version: u.Version,
		}
		e.Sign(s.hmacSecret)
		if err := pub.Publish(ctx, admin.RoutingKeyUserSuspended, e); err != nil {
			return i, fmt.Errorf("publicar la suspensión de %s: %w", u.UserID, err)
		}
	}
	return len(suspended), nil
}
