package handler

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/discordia-grupo01/admin/internal/apierr"
	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/service"
	"github.com/gin-gonic/gin"
)

// maxSearchLength acota el texto de búsqueda para no armar patrones enormes.
const maxSearchLength = 100

// searchQuery lee el parámetro search sin espacios en los bordes. Si supera
// maxSearchLength responde 400 y devuelve false.
func searchQuery(c *gin.Context) (string, bool) {
	search := strings.TrimSpace(c.Query("search"))
	if utf8.RuneCountInString(search) > maxSearchLength {
		apierr.InvalidQuery(c, "search", "El parámetro search no puede superar los 100 caracteres")
		return "", false
	}
	return search, true
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

	list, err := h.users.List(c.Request.Context(), service.ListUsersParams{
		Search: search,
		Status: model.UserStatus(c.Query("status")),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		if errors.Is(err, service.ErrInvalidStatus) {
			apierr.InvalidQuery(c, "status", "El parámetro status debe ser active o suspended")
			return
		}
		log.Printf("handler: no se pudo listar usuarios: %v", err)
		apierr.Internal(c)
		return
	}
	c.JSON(http.StatusOK, list)
}
