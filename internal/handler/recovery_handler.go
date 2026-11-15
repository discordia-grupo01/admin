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

// RecoveryRequests es lo que el handler necesita del servicio de solicitudes
// de recuperación de cuenta.
type RecoveryRequests interface {
	List(ctx context.Context, p service.ListRecoveryRequestsParams) (*model.RecoveryRequestList, error)
	Get(ctx context.Context, requestID string) (*model.RecoveryRequestDetail, error)
	Approve(ctx context.Context, requestID, staffID string) (*model.RecoveryRequestDetail, error)
	Reject(ctx context.Context, requestID, staffID string) (*model.RecoveryRequestDetail, error)
}

type RecoveryHandler struct {
	requests RecoveryRequests
}

func NewRecoveryHandler(requests RecoveryRequests) *RecoveryHandler {
	return &RecoveryHandler{requests: requests}
}

// Register agrega las rutas de solicitudes de recuperación al grupo /v1/admin.
func (h *RecoveryHandler) Register(admin *gin.RouterGroup) {
	admin.GET("/recovery-requests", h.List)
	admin.GET("/recovery-requests/:id", h.Get)
	admin.POST("/recovery-requests/:id/approval", h.Approve)
	admin.POST("/recovery-requests/:id/rejection", h.Reject)
}

// List atiende GET /v1/admin/recovery-requests?search=&status=&limit=&offset=.
//
// @Summary      Listar solicitudes de recuperación de cuenta
// @Description  Pedidos de usuarios con 2FA activo que perdieron la app autenticadora y todos sus códigos de recuperación. Llegan de identify por evento (`users.account_recovery_requested`). Lista paginada, de la más reciente a la más vieja. `search` busca por email o nombre del usuario (parcial, sin distinguir mayúsculas). El `total` con `status=pending` sirve de contador de pendientes.
// @Tags         Recovery
// @Produce      json
// @Security     BearerAuth
// @Param        search  query  string  false  "Texto a buscar en el email o el nombre del usuario. Los caracteres `%`, `_` y `\` se toman como texto literal." maxlength(100)
// @Param        status  query  string  false  "Deja solo las solicitudes en ese estado. Sin este parámetro se listan todas." Enums(pending, approved, rejected)
// @Param        limit   query  int  false  "Tamaño de página (máximo 100; un valor mayor se acota a 100)" minimum(0) maximum(9007199254740991) default(20)
// @Param        offset  query  int  false  "Cantidad de ítems a saltear" minimum(0) maximum(9007199254740991) default(0)
// @Success      200  {object}  model.RecoveryRequestList  "Solicitudes que cumplen los filtros (puede ser una lista vacía)"
// @Failure      400  {object}  apierr.ErrorResponse  "Parámetros de consulta inválidos (`INVALID_QUERY`)"
// @Failure      401  {object}  apierr.ErrorResponse  "Falta el token, es inválido o venció (`SESSION_EXPIRED`)"
// @Failure      403  {object}  apierr.ErrorResponse  "El usuario no es staff de la plataforma (`FORBIDDEN`)"
// @Router       /v1/admin/recovery-requests [get]
func (h *RecoveryHandler) List(c *gin.Context) {
	limit, ok := apierr.ParseNonNegativeIntQuery(c, "limit", model.DefaultLimit)
	if !ok {
		return
	}
	offset, ok := apierr.ParseNonNegativeIntQuery(c, "offset", 0)
	if !ok {
		return
	}
	search, ok := searchQuery(c)
	if !ok {
		return
	}
	status, ok := statusQuery(c)
	if !ok {
		return
	}

	list, err := h.requests.List(c.Request.Context(), service.ListRecoveryRequestsParams{
		Search: search,
		Status: model.RecoveryRequestStatus(status),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		if errors.Is(err, service.ErrInvalidRecoveryStatus) {
			apierr.InvalidQuery(c, "status", "El parámetro status debe ser pending, approved o rejected")
			return
		}
		logging.Errorf("handler: no se pudo listar las solicitudes de recuperación: %v", err)
		apierr.Internal(c)
		return
	}
	c.JSON(http.StatusOK, list)
}

// Get atiende GET /v1/admin/recovery-requests/{id}.
//
// @Summary      Detalle de una solicitud de recuperación de cuenta
// @Description  La solicitud con el usuario y cuántas solicitudes anteriores hizo. Un `{id}` que no es un UUID se trata como inexistente.
// @Tags         Recovery
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  string  true  "ID de la solicitud" format(uuid)
// @Success      200  {object}  model.RecoveryRequestDetail  "La solicitud"
// @Failure      401  {object}  apierr.ErrorResponse  "Falta el token, es inválido o venció (`SESSION_EXPIRED`)"
// @Failure      403  {object}  apierr.ErrorResponse  "El usuario no es staff de la plataforma (`FORBIDDEN`)"
// @Failure      404  {object}  apierr.ErrorResponse  "La solicitud no existe (`RECOVERY_REQUEST_NOT_FOUND`)"
// @Router       /v1/admin/recovery-requests/{id} [get]
func (h *RecoveryHandler) Get(c *gin.Context) {
	detail, err := h.requests.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, "leer", err)
		return
	}
	c.JSON(http.StatusOK, detail)
}

// Approve atiende POST /v1/admin/recovery-requests/{id}/approval. La solicitud
// queda aprobada en admin al instante; identify desactiva el 2FA al procesar
// el evento admin.account_recovery_approved.
//
// @Summary      Aprobar una solicitud de recuperación de cuenta
// @Description  Marca la solicitud como aprobada por el staff que llama y, en la misma transacción, deja escrito `admin.account_recovery_approved` (firmado con HMAC) para que lo publique el outbox. identify, al procesarlo, desactiva el 2FA del usuario, invalida sus códigos de recuperación y le avisa por email. Solo se puede resolver una vez: si dos miembros del staff la resuelven a la vez, uno recibe `409`.
// @Tags         Recovery
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  string  true  "ID de la solicitud" format(uuid)
// @Success      200  {object}  model.RecoveryRequestDetail  "Solicitud aprobada"
// @Failure      401  {object}  apierr.ErrorResponse  "Falta el token, es inválido o venció (`SESSION_EXPIRED`)"
// @Failure      403  {object}  apierr.ErrorResponse  "El usuario no es staff de la plataforma (`FORBIDDEN`)"
// @Failure      404  {object}  apierr.ErrorResponse  "La solicitud no existe (`RECOVERY_REQUEST_NOT_FOUND`)"
// @Failure      409  {object}  apierr.ErrorResponse  "La solicitud ya fue aprobada o rechazada (`RECOVERY_REQUEST_ALREADY_RESOLVED`)"
// @Router       /v1/admin/recovery-requests/{id}/approval [post]
func (h *RecoveryHandler) Approve(c *gin.Context) {
	detail, err := h.requests.Approve(c.Request.Context(), c.Param("id"), middleware.UserID(c))
	if err != nil {
		h.fail(c, "aprobar", err)
		return
	}
	c.JSON(http.StatusOK, detail)
}

// Reject atiende POST /v1/admin/recovery-requests/{id}/rejection.
//
// @Summary      Rechazar una solicitud de recuperación de cuenta
// @Description  Marca la solicitud como rechazada y deja escrito `admin.account_recovery_rejected` (firmado). identify solo le avisa al usuario por email; el 2FA sigue activo y el usuario puede volver a pedirla.
// @Tags         Recovery
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  string  true  "ID de la solicitud" format(uuid)
// @Success      200  {object}  model.RecoveryRequestDetail  "Solicitud rechazada"
// @Failure      401  {object}  apierr.ErrorResponse  "Falta el token, es inválido o venció (`SESSION_EXPIRED`)"
// @Failure      403  {object}  apierr.ErrorResponse  "El usuario no es staff de la plataforma (`FORBIDDEN`)"
// @Failure      404  {object}  apierr.ErrorResponse  "La solicitud no existe (`RECOVERY_REQUEST_NOT_FOUND`)"
// @Failure      409  {object}  apierr.ErrorResponse  "La solicitud ya fue aprobada o rechazada (`RECOVERY_REQUEST_ALREADY_RESOLVED`)"
// @Router       /v1/admin/recovery-requests/{id}/rejection [post]
func (h *RecoveryHandler) Reject(c *gin.Context) {
	detail, err := h.requests.Reject(c.Request.Context(), c.Param("id"), middleware.UserID(c))
	if err != nil {
		h.fail(c, "rechazar", err)
		return
	}
	c.JSON(http.StatusOK, detail)
}

func (h *RecoveryHandler) fail(c *gin.Context, action string, err error) {
	switch {
	case errors.Is(err, service.ErrRecoveryRequestNotFound):
		apierr.Abort(c, http.StatusNotFound, apierr.CodeRecoveryRequestNotFound, "La solicitud de recuperación no existe", nil)
	case errors.Is(err, service.ErrRecoveryRequestResolved):
		apierr.Abort(c, http.StatusConflict, apierr.CodeRecoveryRequestAlreadyResolved, "Esta solicitud ya fue resuelta por otro miembro del staff", nil)
	default:
		logging.Errorf("handler: no se pudo %s la solicitud de recuperación %q: %v", action, c.Param("id"), err)
		apierr.Internal(c)
	}
}
