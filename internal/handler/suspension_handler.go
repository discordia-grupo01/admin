package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/discordia-grupo01/admin/internal/apierr"
	"github.com/discordia-grupo01/admin/internal/logging"
	"github.com/discordia-grupo01/admin/internal/middleware"
	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/service"
	"github.com/gin-gonic/gin"
)

// UserSuspender es lo que el handler necesita del servicio de suspensión.
type UserSuspender interface {
	Suspend(ctx context.Context, userID, staffID string) (*model.User, error)
	Reactivate(ctx context.Context, userID string) (*model.User, error)
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
	admin.DELETE("/users/:id/suspension", h.Reactivate)
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
		userNotFound(c)
	case errors.Is(err, service.ErrCannotSuspendStaff):
		apierr.Abort(c, http.StatusForbidden, apierr.CodeCannotSuspendStaff, "No se puede suspender a un miembro del staff", nil)
	case errors.Is(err, service.ErrUserAlreadySuspended):
		apierr.Abort(c, http.StatusConflict, apierr.CodeUserAlreadySuspended, "El usuario ya está suspendido", nil)
	default:
		logging.Errorf("handler: no se pudo suspender al usuario %q: %v", c.Param("id"), err)
		apierr.Internal(c)
	}
}

// Reactivate atiende DELETE /v1/admin/users/{id}/suspension (HU #30). El estado
// vuelve a activo en admin al instante; los demás servicios levantan el bloqueo
// al procesar el evento admin.user_reactivated.
func (h *SuspensionHandler) Reactivate(c *gin.Context) {
	u, err := h.suspender.Reactivate(c.Request.Context(), c.Param("id"))
	switch {
	case err == nil:
		c.JSON(http.StatusOK, u)
	case errors.Is(err, service.ErrUserNotFound):
		userNotFound(c)
	case errors.Is(err, service.ErrUserNotSuspended):
		apierr.Abort(c, http.StatusConflict, apierr.CodeUserNotSuspended, "El usuario no está suspendido", nil)
	default:
		logging.Errorf("handler: no se pudo reactivar al usuario %q: %v", c.Param("id"), err)
		apierr.Internal(c)
	}
}

func userNotFound(c *gin.Context) {
	apierr.Abort(c, http.StatusNotFound, apierr.CodeUserNotFound, "El usuario no existe", nil)
}
