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
