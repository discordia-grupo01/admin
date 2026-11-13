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

	list, err := h.requests.List(c.Request.Context(), service.ListRecoveryRequestsParams{
		Search: search,
		Status: model.RecoveryRequestStatus(c.Query("status")),
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
func (h *RecoveryHandler) Approve(c *gin.Context) {
	detail, err := h.requests.Approve(c.Request.Context(), c.Param("id"), middleware.UserID(c))
	if err != nil {
		h.fail(c, "aprobar", err)
		return
	}
	c.JSON(http.StatusOK, detail)
}

// Reject atiende POST /v1/admin/recovery-requests/{id}/rejection.
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
