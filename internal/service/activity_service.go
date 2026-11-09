package service

import (
	"context"

	"github.com/discordia-grupo01/admin/internal/repository"
)

// ActivityService aplica los mensajes enviados (messaging.message_created) a la
// actividad del expediente.
type ActivityService struct {
	repo repository.ActivityRepository
}

func NewActivityService(repo repository.ActivityRepository) *ActivityService {
	return &ActivityService{repo: repo}
}

// ApplyMessageCreated registra que el usuario envió un mensaje; repetirlo no
// duplica nada.
func (s *ActivityService) ApplyMessageCreated(ctx context.Context, m repository.MessageActivity) error {
	return s.repo.InsertMessage(ctx, m)
}
