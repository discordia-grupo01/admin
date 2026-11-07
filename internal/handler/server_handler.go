package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/discordia-grupo01/admin/internal/apierr"
	"github.com/discordia-grupo01/admin/internal/logging"
	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/service"
	"github.com/gin-gonic/gin"
)

// ServerLister es lo que el handler necesita del servicio de servidores.
type ServerLister interface {
	List(ctx context.Context, p service.ListServersParams) (*model.ServerList, error)
}

type ServerHandler struct {
	servers ServerLister
}

func NewServerHandler(servers ServerLister) *ServerHandler {
	return &ServerHandler{servers: servers}
}

// Register agrega las rutas de servidores al grupo /v1/admin.
func (h *ServerHandler) Register(admin *gin.RouterGroup) {
	admin.GET("/servers", h.List)
}

// List atiende GET /v1/admin/servers?search=&status=&limit=&offset= (HU #32).
// status (active o suspended) es opcional; sin él se listan todos.
func (h *ServerHandler) List(c *gin.Context) {
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

	list, err := h.servers.List(c.Request.Context(), service.ListServersParams{
		Search: search,
		Status: model.ServerStatus(c.Query("status")),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		if errors.Is(err, service.ErrInvalidServerStatus) {
			apierr.InvalidQuery(c, "status", "El parámetro status debe ser active o suspended")
			return
		}
		logging.Errorf("handler: no se pudo listar servidores: %v", err)
		apierr.Internal(c)
		return
	}
	c.JSON(http.StatusOK, list)
}
