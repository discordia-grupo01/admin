package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/discordia-grupo01/admin/internal/apierr"
	"github.com/discordia-grupo01/admin/internal/logging"
	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/service"
	"github.com/gin-gonic/gin"
)

// maxSearchLength acota el texto de búsqueda para no armar patrones enormes.
const maxSearchLength = 100

// searchQuery lee el parámetro search sin espacios en los bordes. Si supera
// maxSearchLength responde 400 y devuelve false.
func searchQuery(c *gin.Context) (string, bool) {
	// Postgres no admite NUL en un texto: se descarta en vez de fallar con 500.
	search := strings.TrimSpace(strings.ReplaceAll(c.Query("search"), "\x00", ""))
	if utf8.RuneCountInString(search) > maxSearchLength {
		apierr.InvalidQuery(c, "search", "El parámetro search no puede superar los 100 caracteres")
		return "", false
	}
	return search, true
}

// statusQuery lee el filtro status. Si viene el parámetro pero vacío responde
// 400: para no filtrar hay que omitirlo.
func statusQuery(c *gin.Context) (string, bool) {
	status, present := c.GetQuery("status")
	if present && status == "" {
		apierr.InvalidQuery(c, "status", "El parámetro status no puede estar vacío: omitilo para no filtrar")
		return "", false
	}
	return status, true
}

// UserLister es lo que el handler necesita del servicio de usuarios.
type UserLister interface {
	List(ctx context.Context, p service.ListUsersParams) (*model.UserList, error)
}

type UserHandler struct {
	users UserLister
}

func NewUserHandler(users UserLister) *UserHandler {
	return &UserHandler{users: users}
}

// Register agrega las rutas de usuarios al grupo /v1/admin.
func (h *UserHandler) Register(admin *gin.RouterGroup) {
	admin.GET("/users", h.List)
}

// List atiende GET /v1/admin/users?search=&status=&limit=&offset= (HU #31).
//
// @Summary      Listar usuarios con filtros (HU #31)
// @Description  Lista paginada, ordenada por fecha de registro descendente. `search` busca por email o nombre (parcial, sin distinguir mayúsculas).
// @Tags         Users
// @Produce      json
// @Security     BearerAuth
// @Param        search  query  string  false  "Texto a buscar en el email o el nombre. Los caracteres `%`, `_` y `\` se toman como texto literal." maxlength(100)
// @Param        status  query  string  false  "Deja solo los usuarios en ese estado. Sin este parámetro se listan todos." Enums(active, pending, suspended)
// @Param        limit   query  int  false  "Tamaño de página (máximo 100; un valor mayor se acota a 100)" minimum(0) maximum(9007199254740991) default(20)
// @Param        offset  query  int  false  "Cantidad de ítems a saltear" minimum(0) maximum(9007199254740991) default(0)
// @Success      200  {object}  model.UserList  "Usuarios que cumplen los filtros (puede ser una lista vacía)"
// @Failure      400  {object}  apierr.ErrorResponse  "Parámetros de consulta inválidos (`INVALID_QUERY`)"
// @Failure      401  {object}  apierr.ErrorResponse  "Falta el token, es inválido o venció (`SESSION_EXPIRED`)"
// @Failure      403  {object}  apierr.ErrorResponse  "El usuario no es staff de la plataforma (`FORBIDDEN`)"
// @Router       /v1/admin/users [get]
func (h *UserHandler) List(c *gin.Context) {
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

	list, err := h.users.List(c.Request.Context(), service.ListUsersParams{
		Search: search,
		Status: model.UserStatus(status),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		if errors.Is(err, service.ErrInvalidStatus) {
			apierr.InvalidQuery(c, "status", "El parámetro status debe ser active, pending o suspended")
			return
		}
		logging.Errorf("handler: no se pudo listar usuarios: %v", err)
		apierr.Internal(c)
		return
	}
	c.JSON(http.StatusOK, list)
}
