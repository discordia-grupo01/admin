package handler

import (
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/discordia-grupo01/admin/internal/apierr"
	"github.com/discordia-grupo01/admin/internal/middleware"
	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/service"
	"github.com/gin-gonic/gin"
)

// UserSuspender es lo que el handler necesita del servicio de suspensión.
type UserSuspender interface {
	Suspend(ctx context.Context, userID, staffID string) (*model.User, error)
}

type SuspensionHandler struct {
	suspender UserSuspender
}

func NewSuspensionHandler(suspender UserSuspender) *SuspensionHandler {
	return &SuspensionHandler{suspender: suspender}
}

// Register agrega las rutas de suspensión al grupo /v1/admin.
func (h *SuspensionHandler) Register(admin *gin.RouterGroup) {
	admin.POST("/users/:id/suspension", h.Suspend)
}

// Suspend atiende POST /v1/admin/users/{id}/suspension (HU #29). La suspensión
// se aplica en admin al instante; los demás servicios la aplican al procesar
// el evento admin.user_suspended.
func (h *SuspensionHandler) Suspend(c *gin.Context) {
	u, err := h.suspender.Suspend(c.Request.Context(), c.Param("id"), middleware.UserID(c))
	switch {
	case err == nil:
		c.JSON(http.StatusOK, u)
	case errors.Is(err, service.ErrUserNotFound):
		apierr.Abort(c, http.StatusNotFound, apierr.CodeUserNotFound, "El usuario no existe", nil)
	case errors.Is(err, service.ErrCannotSuspendStaff):
		apierr.Abort(c, http.StatusForbidden, apierr.CodeCannotSuspendStaff, "No se puede suspender a un miembro del staff", nil)
	case errors.Is(err, service.ErrUserAlreadySuspended):
		apierr.Abort(c, http.StatusConflict, apierr.CodeUserAlreadySuspended, "El usuario ya está suspendido", nil)
	default:
		log.Printf("handler: no se pudo suspender al usuario %q: %v", c.Param("id"), err)
		apierr.Internal(c)
	}
}
