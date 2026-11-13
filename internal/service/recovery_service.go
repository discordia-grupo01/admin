package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/events-contract/admin"
	"github.com/discordia-grupo01/events-contract/envelope"
	"github.com/google/uuid"
)

var (
	ErrRecoveryRequestNotFound = repository.ErrRecoveryRequestNotFound
	ErrRecoveryRequestResolved = repository.ErrRecoveryRequestResolved
	// ErrInvalidRecoveryStatus indica un filtro de estado que no es pending, approved ni rejected.
	ErrInvalidRecoveryStatus = errors.New("estado inválido: debe ser pending, approved o rejected")
)

type ListRecoveryRequestsParams struct {
	Search string
	// Status vacío = todas.
	Status model.RecoveryRequestStatus
	Limit  int
	Offset int
}

// RecoveryService atiende las solicitudes de recuperación de cuenta: las
// guarda cuando llegan de identify, las lista para el staff y publica la
// decisión firmada para que identify la aplique.
type RecoveryService struct {
	repo       repository.RecoveryRequestRepository
	hmacSecret string
	now        func() time.Time
	newEventID func() string
}

// NewRecoveryService recibe el secreto con el que se firman los eventos admin.*.
func NewRecoveryService(repo repository.RecoveryRequestRepository, hmacSecret string) *RecoveryService {
	return &RecoveryService{repo: repo, hmacSecret: hmacSecret, now: time.Now, newEventID: uuid.NewString}
}

// ApplyRequest guarda la solicitud que llegó en users.account_recovery_requested.
func (s *RecoveryService) ApplyRequest(ctx context.Context, r repository.RecoveryRequestRecord) error {
	return s.repo.Insert(ctx, r)
}

// List devuelve una página de solicitudes. El límite se normaliza (default 20,
// máximo 100).
func (s *RecoveryService) List(ctx context.Context, p ListRecoveryRequestsParams) (*model.RecoveryRequestList, error) {
	if p.Status != "" && !p.Status.Valid() {
		return nil, ErrInvalidRecoveryStatus
	}
	limit := model.NormalizeLimit(p.Limit)

	requests, total, err := s.repo.List(ctx, repository.RecoveryRequestFilter{Search: p.Search, Status: p.Status}, limit, p.Offset)
	if err != nil {
		return nil, fmt.Errorf("listar solicitudes de recuperación: %w", err)
	}
	return &model.RecoveryRequestList{Requests: requests, Total: total, Limit: limit, Offset: p.Offset}, nil
}

// Get devuelve una solicitud con sus anteriores. Un id que no es un UUID no
// puede existir, así que se trata como inexistente.
func (s *RecoveryService) Get(ctx context.Context, requestID string) (*model.RecoveryRequestDetail, error) {
	id, ok := canonicalID(requestID)
	if !ok {
		return nil, ErrRecoveryRequestNotFound
	}
	return s.repo.Get(ctx, id)
}

// Approve aprueba la solicitud en nombre del staff staffID y deja en el outbox
// admin.account_recovery_approved, firmado: identify le desactiva el 2FA al
// usuario y le avisa por email.
func (s *RecoveryService) Approve(ctx context.Context, requestID, staffID string) (*model.RecoveryRequestDetail, error) {
	return s.resolve(ctx, requestID, staffID, model.RecoveryRequestApproved)
}

// Reject rechaza la solicitud y deja en el outbox admin.account_recovery_rejected,
// firmado: identify solo le avisa al usuario.
func (s *RecoveryService) Reject(ctx context.Context, requestID, staffID string) (*model.RecoveryRequestDetail, error) {
	return s.resolve(ctx, requestID, staffID, model.RecoveryRequestRejected)
}

func (s *RecoveryService) resolve(ctx context.Context, requestID, staffID string, status model.RecoveryRequestStatus) (*model.RecoveryRequestDetail, error) {
	id, ok := canonicalID(requestID)
	if !ok {
		return nil, ErrRecoveryRequestNotFound
	}

	at := s.now().UTC().Truncate(time.Microsecond)
	detail, err := s.repo.Resolve(ctx, id, staffID, status, at, func(userID string) (string, any) {
		meta := envelope.Meta{EventID: s.newEventID(), OccurredAt: at}
		if status == model.RecoveryRequestApproved {
			e := admin.AccountRecoveryApproved{Meta: meta, RequestID: id, UserID: userID}
			e.Sign(s.hmacSecret)
			return admin.RoutingKeyAccountRecoveryApproved, e
		}
		e := admin.AccountRecoveryRejected{Meta: meta, RequestID: id, UserID: userID}
		e.Sign(s.hmacSecret)
		return admin.RoutingKeyAccountRecoveryRejected, e
	})
	if err != nil {
		return nil, fmt.Errorf("resolver la solicitud de recuperación: %w", err)
	}
	return detail, nil
}
