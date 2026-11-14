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
//
// @Summary      Listar los servidores de la plataforma (HU #32)
// @Description  Lista paginada, ordenada por fecha de creación descendente. Un servidor aparece cuando llega su alta a la réplica y desaparece cuando se elimina. Los suspendidos siguen apareciendo, con `suspended_at` informado.
// @Tags         Servers
// @Produce      json
// @Security     BearerAuth
// @Param        search  query  string  false  "Texto a buscar en el nombre del servidor (parcial, sin distinguir mayúsculas). Los caracteres `%`, `_` y `\` se toman como texto literal." maxlength(100)
// @Param        status  query  string  false  "Deja solo los servidores activos o solo los suspendidos. Sin este parámetro se listan todos; otro valor da `400 INVALID_QUERY`." Enums(active, suspended)
// @Param        limit   query  int  false  "Tamaño de página (máximo 100; un valor mayor se acota a 100)" minimum(0) maximum(100) default(20)
// @Param        offset  query  int  false  "Cantidad de ítems a saltear" minimum(0) default(0)
// @Success      200  {object}  model.ServerList  "Servidores que cumplen el filtro (puede ser una lista vacía)"
// @Failure      400  {object}  apierr.ErrorResponse  "Parámetros de consulta inválidos (`INVALID_QUERY`)"
// @Failure      401  {object}  apierr.ErrorResponse  "Falta el token, es inválido o venció (`SESSION_EXPIRED`)"
// @Failure      403  {object}  apierr.ErrorResponse  "El usuario no es staff de la plataforma (`FORBIDDEN`)"
// @Router       /v1/admin/servers [get]
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
