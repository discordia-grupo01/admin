package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/google/uuid"
)

// ErrInvalidSection indica una sección que el expediente no tiene.
var ErrInvalidSection = errors.New("sección inválida")

// unknownServer reemplaza al nombre de un servidor que admin todavía no conoce.
const unknownServer = "un servidor"

// DossierService arma el expediente de investigación de un usuario o un
// servidor, solo con datos locales (las réplicas de admin).
type DossierService struct {
	repo     repository.DossierRepository
	recovery RecoveryRequestReader
	now      func() time.Time
}

// RecoveryRequestReader lee las solicitudes de recuperación de un usuario.
type RecoveryRequestReader interface {
	UserRequests(ctx context.Context, userID string, limit, offset int) ([]model.RecoveryRequest, int, error)
}

// WithRecoveryRequests suma la sección recovery_requests al expediente de
// usuario. Sin esto la sección queda vacía.
func (s *DossierService) WithRecoveryRequests(recovery RecoveryRequestReader) *DossierService {
	s.recovery = recovery
	return s
}

func (s *DossierService) userRecoveryRequests(ctx context.Context, userID string, limit, offset int) ([]model.DossierRecoveryRequest, int, error) {
	if s.recovery == nil {
		return []model.DossierRecoveryRequest{}, 0, nil
	}
	requests, total, err := s.recovery.UserRequests(ctx, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	out := make([]model.DossierRecoveryRequest, 0, len(requests))
	for _, r := range requests {
		out = append(out, model.DossierRecoveryRequest{
			ID: r.ID, Status: r.Status, RequestedAt: r.RequestedAt, ResolvedAt: r.ResolvedAt, ResolvedBy: r.ResolvedBy,
		})
	}
	return out, total, nil
}

func NewDossierService(repo repository.DossierRepository) *DossierService {
	return &DossierService{repo: repo, now: time.Now}
}

// canonicalID devuelve el UUID en minúsculas. Un id que no es un UUID no puede
// existir, así que quien llama lo trata como inexistente.
func canonicalID(raw string) (string, bool) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return "", false
	}
	return id.String(), true
}

func validUserSection(s string) bool {
	switch s {
	case model.SectionServers, model.SectionActivity, model.SectionReports, model.SectionModeration, model.SectionRecoveryRequests:
		return true
	}
	return false
}

func validServerSection(s string) bool {
	return s == model.SectionReports || s == model.SectionModeration
}

// UserSummary devuelve el resumen del expediente: el usuario, su última
// actividad y, de cada sección, el total y los 5 ítems más recientes.
func (s *DossierService) UserSummary(ctx context.Context, userID string) (*model.UserDossierSummary, error) {
	id, ok := canonicalID(userID)
	if !ok {
		return nil, ErrUserNotFound
	}
	user, err := s.repo.FindUser(ctx, id)
	if err != nil {
		return nil, err
	}
	last, err := s.repo.UserLastActivity(ctx, id)
	if err != nil {
		return nil, err
	}

	const n = model.DossierPreviewSize
	out := &model.UserDossierSummary{User: user, LastActivityAt: last}
	if out.Latest.Servers, out.Counts.Servers, err = s.repo.UserMemberships(ctx, id, n, 0); err != nil {
		return nil, err
	}
	entries, total, err := s.repo.UserActivity(ctx, id, n, 0)
	if err != nil {
		return nil, err
	}
	out.Latest.Activity, out.Counts.Activity = describeActivity(entries), total
	if out.Latest.Reports, out.Counts.Reports, err = s.repo.UserReports(ctx, id, n, 0); err != nil {
		return nil, err
	}
	if out.Latest.Moderation, out.Counts.Moderation, err = s.repo.UserModeration(ctx, id, n, 0); err != nil {
		return nil, err
	}
	if out.Latest.RecoveryRequests, out.Counts.RecoveryRequests, err = s.userRecoveryRequests(ctx, id, n, 0); err != nil {
		return nil, err
	}
	return out, nil
}

// UserSection devuelve una página de una sección del expediente del usuario:
// servers, activity, reports, moderation o recovery_requests. Una sección desconocida da
// ErrInvalidSection antes de consultar nada.
func (s *DossierService) UserSection(ctx context.Context, userID, section string, limit, offset int) (any, error) {
	if !validUserSection(section) {
		return nil, ErrInvalidSection
	}
	id, ok := canonicalID(userID)
	if !ok {
		return nil, ErrUserNotFound
	}
	if _, err := s.repo.FindUser(ctx, id); err != nil {
		return nil, err
	}
	limit = model.NormalizeLimit(limit)

	switch section {
	case model.SectionServers:
		items, total, err := s.repo.UserMemberships(ctx, id, limit, offset)
		return page(items, total, limit, offset, err)
	case model.SectionActivity:
		entries, total, err := s.repo.UserActivity(ctx, id, limit, offset)
		return page(describeActivity(entries), total, limit, offset, err)
	case model.SectionReports:
		items, total, err := s.repo.UserReports(ctx, id, limit, offset)
		return page(items, total, limit, offset, err)
	case model.SectionRecoveryRequests:
		items, total, err := s.userRecoveryRequests(ctx, id, limit, offset)
		return page(items, total, limit, offset, err)
	default:
		items, total, err := s.repo.UserModeration(ctx, id, limit, offset)
		return page(items, total, limit, offset, err)
	}
}

// ServerSummary devuelve el resumen del expediente: el servidor, sus 7 días de
// mensajería y, de reportes y moderación, el total y los 5 ítems más recientes.
func (s *DossierService) ServerSummary(ctx context.Context, serverID string) (*model.ServerDossierSummary, error) {
	id, ok := canonicalID(serverID)
	if !ok {
		return nil, ErrServerNotFound
	}
	server, err := s.repo.FindServer(ctx, id)
	if err != nil {
		return nil, err
	}
	messaging, err := s.repo.ServerMessaging(ctx, id, s.now())
	if err != nil {
		return nil, err
	}

	const n = model.DossierPreviewSize
	out := &model.ServerDossierSummary{Server: server, Messaging: messaging}
	if out.Latest.Reports, out.Counts.Reports, err = s.repo.ServerReports(ctx, id, n, 0); err != nil {
		return nil, err
	}
	if out.Latest.Moderation, out.Counts.Moderation, err = s.repo.ServerModeration(ctx, id, n, 0); err != nil {
		return nil, err
	}
	return out, nil
}

// ServerSection devuelve una página de reports o moderation del servidor.
func (s *DossierService) ServerSection(ctx context.Context, serverID, section string, limit, offset int) (any, error) {
	if !validServerSection(section) {
		return nil, ErrInvalidSection
	}
	id, ok := canonicalID(serverID)
	if !ok {
		return nil, ErrServerNotFound
	}
	if _, err := s.repo.FindServer(ctx, id); err != nil {
		return nil, err
	}
	limit = model.NormalizeLimit(limit)

	if section == model.SectionReports {
		items, total, err := s.repo.ServerReports(ctx, id, limit, offset)
		return page(items, total, limit, offset, err)
	}
	items, total, err := s.repo.ServerModeration(ctx, id, limit, offset)
	return page(items, total, limit, offset, err)
}

func page[T any](items []T, total, limit, offset int, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []T{}
	}
	return model.DossierPage[T]{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

// describeActivity arma el texto de cada actividad. Nunca incluye el contenido
// de un mensaje: admin no lo tiene.
func describeActivity(entries []repository.ActivityEntry) []model.DossierActivity {
	out := make([]model.DossierActivity, 0, len(entries))
	for _, e := range entries {
		server := e.ServerName
		if server == "" {
			server = unknownServer
		}
		var text string
		switch e.Kind {
		case model.ActivityMessageSent:
			if e.ChannelName != "" {
				text = fmt.Sprintf("Envió un mensaje en #%s de %s", e.ChannelName, server)
			} else {
				text = fmt.Sprintf("Envió un mensaje en un canal de %s", server)
			}
		case model.ActivityJoinedServer:
			text = "Se unió a " + server
		case model.ActivityLeftServer:
			text = "Salió de " + server
		default:
			text = e.Kind
		}
		out = append(out, model.DossierActivity{
			ID: e.ID, Kind: e.Kind, Description: text, ServerID: e.ServerID, OccurredAt: e.OccurredAt,
		})
	}
	return out
}
