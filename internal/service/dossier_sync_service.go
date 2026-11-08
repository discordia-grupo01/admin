package service

import (
	"context"

	"github.com/discordia-grupo01/admin/internal/repository"
)

// DossierSyncService aplica los eventos de servers que alimentan el expediente
// (reportes y acciones de moderación de los servidores).
type DossierSyncService struct {
	repo repository.DossierSyncRepository
}

func NewDossierSyncService(repo repository.DossierSyncRepository) *DossierSyncService {
	return &DossierSyncService{repo: repo}
}

// ApplyReportUpdated guarda la foto del reporte (servers.report_updated); se
// ignora si no es más nueva que la guardada.
func (s *DossierSyncService) ApplyReportUpdated(ctx context.Context, r repository.ReportSnapshot) error {
	return s.repo.UpsertReport(ctx, r)
}

// ApplyModerationActionRecorded guarda la acción de un moderador
// (servers.moderation_action_recorded); repetirla no duplica nada.
func (s *DossierSyncService) ApplyModerationActionRecorded(ctx context.Context, a repository.ServerModerationAction) error {
	return s.repo.InsertServerAction(ctx, a)
}
