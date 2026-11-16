package handler

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/discordia-grupo01/admin/internal/apierr"
	"github.com/discordia-grupo01/admin/internal/logging"
	"github.com/discordia-grupo01/admin/internal/middleware"
	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/service"
	"github.com/gin-gonic/gin"
)

// ServerModerator es lo que el handler necesita del servicio de moderación de servidores.
type ServerModerator interface {
	Delete(ctx context.Context, serverID, confirmName, staffID string) error
	Suspend(ctx context.Context, serverID, staffID string) (*model.Server, error)
	Reactivate(ctx context.Context, serverID string) (*model.Server, error)
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
	admin.POST("/servers/:id/suspension", h.Suspend)
	admin.DELETE("/servers/:id/suspension", h.Reactivate)
}

type deleteServerRequest struct {
	// ConfirmName es el nombre del servidor, tal como lo ve el staff. Sin espacios
	// en los bordes; distingue mayúsculas.
	ConfirmName string `json:"confirm_name" example:"Sala de prueba"`
}

// Delete atiende DELETE /v1/admin/servers/{id} (HU #68). Responde 202 porque
// admin solo registra el pedido y publica admin.server_deletion_requested: el
// borrado lo hace servers y el servidor sale del listado cuando lo confirma con
// servers.server_deleted.
//
// El cuerpo trae confirm_name, que tiene que ser el nombre del servidor. Si
// falta, el servicio lo recibe vacío y responde que no coincide: nunca se borra
// sin confirmación.
//
// @Summary      Eliminar un servidor desde el backoffice (HU #68)
// @Description  Pide la eliminación definitiva del servidor con todos sus canales, mensajes y roles. El staff confirma escribiendo el nombre del servidor (`confirm_name`), que se compara sin espacios en los bordes y distinguiendo mayúsculas contra el nombre que admin tiene replicado.
// @Description
// @Description  Admin **no borra nada**: registra el pedido y, en la misma transacción, deja escrito el evento `admin.server_deletion_requested` (firmado con HMAC) para que lo publique el outbox. `servers` ejecuta el borrado y avisa con `servers.server_deleted`; recién entonces el servidor desaparece de `GET /v1/admin/servers`. Hasta ese momento (normalmente unos segundos) sigue en el listado. Si el evento se pierde el servidor no se borra y el pedido se puede repetir: cada pedido publica un evento con una `version` mayor y `servers` es idempotente.
// @Description
// @Description  Es irreversible. Un `{id}` que no es un UUID se trata como servidor inexistente. Sin `confirm_name` o con un nombre distinto no se hace nada.
// @Tags         Servers
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  string  true  "ID del servidor" format(uuid)
// @Param        body  body  deleteServerRequest  true  "Confirmación con el nombre del servidor"
// @Success      202  "Pedido aceptado; la eliminación se completa de forma asíncrona. Sin cuerpo."
// @Failure      400  {object}  apierr.ErrorResponse  "El cuerpo no es un JSON válido (`INVALID_BODY`) o `confirm_name` falta o no es el nombre del servidor (`DELETE_CONFIRMATION_MISMATCH`, con `details.field = confirm_name`)"
// @Failure      401  {object}  apierr.ErrorResponse  "Falta el token, es inválido o venció (`SESSION_EXPIRED`)"
// @Failure      403  {object}  apierr.ErrorResponse  "El usuario no es staff de la plataforma (`FORBIDDEN`)"
// @Failure      404  {object}  apierr.ErrorResponse  "El servidor no existe, todavía no llegó a la réplica o ya fue eliminado (`SERVER_NOT_FOUND`)"
// @Router       /v1/admin/servers/{id} [delete]
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
		logging.Infof("handler: el staff %s pidió eliminar el servidor %s", staffID, c.Param("id"))
		c.Status(http.StatusAccepted)
	case errors.Is(err, service.ErrServerNotFound):
		serverNotFound(c)
	case errors.Is(err, service.ErrDeleteConfirmationMismatch):
		apierr.Abort(c, http.StatusBadRequest, apierr.CodeDeleteConfirmationMismatch,
			"El nombre ingresado no coincide con el del servidor", map[string]any{"field": "confirm_name"})
	default:
		logging.Errorf("handler: no se pudo pedir la eliminación del servidor %q: %v", c.Param("id"), err)
		apierr.Internal(c)
	}
}

// Suspend atiende POST /v1/admin/servers/{id}/suspension (HU #69). La suspensión
// se aplica en admin al instante; servers, messaging y voz la aplican al
// procesar el evento admin.server_suspended.
//
// @Summary      Suspender un servidor (HU #69)
// @Description  Bloquea el uso del servidor sin eliminarlo: ningún miembro, ni su owner, puede leer, escribir, entrar a voz, invitar, editar, transferir ni eliminarlo (salvo abandonarlo), pero su contenido se conserva. El estado cambia en admin al instante y, en la misma transacción, queda escrito el evento `admin.server_suspended` (firmado con HMAC) para que lo publique el outbox; servers, messaging y voz lo aplican a los pocos instantes. El servidor sigue en `GET /v1/admin/servers`, con `suspended_at` informado.
// @Description
// @Description  Se puede suspender un servidor de cualquier dueño. Un `{id}` que no es un UUID se trata como servidor inexistente.
// @Tags         Servers
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  string  true  "ID del servidor" format(uuid)
// @Success      200  {object}  model.Server  "Servidor suspendido"
// @Failure      401  {object}  apierr.ErrorResponse  "Falta el token, es inválido o venció (`SESSION_EXPIRED`)"
// @Failure      403  {object}  apierr.ErrorResponse  "El usuario no es staff de la plataforma (`FORBIDDEN`)"
// @Failure      404  {object}  apierr.ErrorResponse  "El servidor no existe, todavía no llegó a la réplica o ya fue eliminado (`SERVER_NOT_FOUND`)"
// @Failure      409  {object}  apierr.ErrorResponse  "El servidor ya estaba suspendido (`SERVER_ALREADY_SUSPENDED`)"
// @Router       /v1/admin/servers/{id}/suspension [post]
func (h *ServerModerationHandler) Suspend(c *gin.Context) {
	staffID := middleware.UserID(c)
	s, err := h.moderator.Suspend(c.Request.Context(), c.Param("id"), staffID)
	switch {
	case err == nil:
		logging.Infof("handler: el staff %s suspendió el servidor %s", staffID, c.Param("id"))
		c.JSON(http.StatusOK, s)
	case errors.Is(err, service.ErrServerNotFound):
		serverNotFound(c)
	case errors.Is(err, service.ErrServerAlreadySuspended):
		apierr.Abort(c, http.StatusConflict, apierr.CodeServerAlreadySuspended, "El servidor ya está suspendido", nil)
	default:
		logging.Errorf("handler: no se pudo suspender el servidor %q: %v", c.Param("id"), err)
		apierr.Internal(c)
	}
}

// Reactivate atiende DELETE /v1/admin/servers/{id}/suspension (HU #69). El
// servidor vuelve a estar activo en admin al instante; los demás servicios
// levantan el bloqueo al procesar el evento admin.server_reactivated.
//
// @Summary      Levantar la suspensión de un servidor (HU #69)
// @Description  Sus miembros recuperan el acceso normal. El servidor vuelve a estar activo en admin al instante y, en la misma transacción, queda escrito el evento `admin.server_reactivated` (firmado con HMAC), con una `version` mayor que la de la suspensión. Los demás servicios dejan de bloquearlo al procesarlo. Un `{id}` que no es un UUID se trata como servidor inexistente.
// @Tags         Servers
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  string  true  "ID del servidor" format(uuid)
// @Success      200  {object}  model.Server  "Servidor reactivado"
// @Failure      401  {object}  apierr.ErrorResponse  "Falta el token, es inválido o venció (`SESSION_EXPIRED`)"
// @Failure      403  {object}  apierr.ErrorResponse  "El usuario no es staff de la plataforma (`FORBIDDEN`)"
// @Failure      404  {object}  apierr.ErrorResponse  "El servidor no existe, todavía no llegó a la réplica o ya fue eliminado (`SERVER_NOT_FOUND`)"
// @Failure      409  {object}  apierr.ErrorResponse  "El servidor no estaba suspendido (`SERVER_NOT_SUSPENDED`)"
// @Router       /v1/admin/servers/{id}/suspension [delete]
func (h *ServerModerationHandler) Reactivate(c *gin.Context) {
	s, err := h.moderator.Reactivate(c.Request.Context(), c.Param("id"))
	switch {
	case err == nil:
		logging.Infof("handler: el staff %s reactivó el servidor %s", middleware.UserID(c), c.Param("id"))
		c.JSON(http.StatusOK, s)
	case errors.Is(err, service.ErrServerNotFound):
		serverNotFound(c)
	case errors.Is(err, service.ErrServerNotSuspended):
		apierr.Abort(c, http.StatusConflict, apierr.CodeServerNotSuspended, "El servidor no está suspendido", nil)
	default:
		logging.Errorf("handler: no se pudo reactivar el servidor %q: %v", c.Param("id"), err)
		apierr.Internal(c)
	}
}

func serverNotFound(c *gin.Context) {
	apierr.Abort(c, http.StatusNotFound, apierr.CodeServerNotFound, "El servidor no existe", nil)
}
