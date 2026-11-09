package service

import (
	"context"
	"time"

	"github.com/discordia-grupo01/admin/internal/logging"
	"github.com/discordia-grupo01/admin/internal/repository"
)

// ActivityPruner borra periódicamente la actividad más vieja que la retención.
// Acota el tamaño de user_activity (un registro por mensaje) y, con eso, hasta
// cuándo se puede atribuir un reporte de mensaje a su autor.
type ActivityPruner struct {
	repo          repository.ActivityRepository
	retentionDays int
	interval      time.Duration
	now           func() time.Time
}

func NewActivityPruner(repo repository.ActivityRepository, retentionDays int, interval time.Duration) *ActivityPruner {
	return &ActivityPruner{repo: repo, retentionDays: retentionDays, interval: interval, now: time.Now}
}

// Start poda una vez al arrancar y después cada interval, hasta que se cancele
// ctx. Una falla solo se loguea: se reintenta en el próximo tick.
func (p *ActivityPruner) Start(ctx context.Context) {
	p.Prune(ctx)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.Prune(ctx)
		}
	}
}

// Prune borra la actividad anterior a la retención y devuelve cuántas filas.
func (p *ActivityPruner) Prune(ctx context.Context) int64 {
	cutoff := p.now().UTC().AddDate(0, 0, -p.retentionDays)
	n, err := p.repo.Prune(ctx, cutoff)
	if err != nil {
		logging.Warnf("actividad: no se pudo aplicar la retención de %d días, se reintenta en %s: %v", p.retentionDays, p.interval, err)
		return 0
	}
	if n > 0 {
		logging.Infof("actividad: se borraron %d registros de más de %d días", n, p.retentionDays)
	}
	return n
}
