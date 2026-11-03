package handler

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/discordia-grupo01/admin/internal/apierr"
	"github.com/discordia-grupo01/admin/internal/middleware"
	"github.com/discordia-grupo01/admin/internal/service"
	"github.com/gin-gonic/gin"
)

// ServerModerator es lo que el handler necesita del servicio de moderación de servidores.
type ServerModerator interface {
	Delete(ctx context.Context, serverID, confirmName, staffID string) error
}

type ServerModerationHandler struct {
	moderator ServerModerator
}

func NewServerModerationHandler(moderator ServerModerator) *ServerModerationHandler {
	return &ServerModerationHandler{moderator: moderator}
}

// Register agrega las rutas de moderación de servidores al grupo /v1/admin.
func (h *ServerModerationHandler) Register(admin *gin.RouterGroup) {
	admin.DELETE("/servers/:id", h.Delete)
}

type deleteServerRequest struct {
	ConfirmName string `json:"confirm_name"`
}

// Delete atiende DELETE /v1/admin/servers/{id} (HU #68). Responde 202 porque
// admin solo registra el pedido y publica admin.server_deletion_requested: el
// borrado lo hace servers y el servidor sale del listado cuando lo confirma con
// servers.server_deleted.
//
// El cuerpo trae confirm_name, que tiene que ser el nombre del servidor. Si
// falta, el servicio lo recibe vacío y responde que no coincide: nunca se borra
// sin confirmación.
func (h *ServerModerationHandler) Delete(c *gin.Context) {
	var body deleteServerRequest
	if err := c.ShouldBindJSON(&body); err != nil && !errors.Is(err, io.EOF) {
		apierr.Abort(c, http.StatusBadRequest, apierr.CodeInvalidBody, "El cuerpo de la request no es válido", nil)
		return
	}

	staffID := middleware.UserID(c)
	err := h.moderator.Delete(c.Request.Context(), c.Param("id"), body.ConfirmName, staffID)
	switch {
	case err == nil:
		log.Printf("handler: el staff %s pidió eliminar el servidor %s", staffID, c.Param("id"))
		c.Status(http.StatusAccepted)
	case errors.Is(err, service.ErrServerNotFound):
		serverNotFound(c)
	case errors.Is(err, service.ErrDeleteConfirmationMismatch):
		apierr.Abort(c, http.StatusBadRequest, apierr.CodeDeleteConfirmationMismatch,
			"El nombre ingresado no coincide con el del servidor", map[string]any{"field": "confirm_name"})
	default:
		log.Printf("handler: no se pudo pedir la eliminación del servidor %q: %v", c.Param("id"), err)
		apierr.Internal(c)
	}
}

func serverNotFound(c *gin.Context) {
	apierr.Abort(c, http.StatusNotFound, apierr.CodeServerNotFound, "El servidor no existe", nil)
}
