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

// DossierReader es lo que el handler necesita del servicio del expediente.
type DossierReader interface {
	UserSummary(ctx context.Context, userID string) (*model.UserDossierSummary, error)
	UserSection(ctx context.Context, userID, section string, limit, offset int) (any, error)
	ServerSummary(ctx context.Context, serverID string) (*model.ServerDossierSummary, error)
	ServerSection(ctx context.Context, serverID, section string, limit, offset int) (any, error)
}

type DossierHandler struct {
	dossier DossierReader
}

func NewDossierHandler(dossier DossierReader) *DossierHandler {
	return &DossierHandler{dossier: dossier}
}

// Register agrega las rutas del expediente al grupo /v1/admin, que ya exige un
// JWT válido y el rol de staff: un usuario común recibe 403 sin leer nada.
func (h *DossierHandler) Register(admin *gin.RouterGroup) {
	admin.GET("/users/:id/dossier", h.UserSummary)
	admin.GET("/users/:id/dossier/:section", h.UserSection)
	admin.GET("/servers/:id/dossier", h.ServerSummary)
	admin.GET("/servers/:id/dossier/:section", h.ServerSection)
}

// UserSummary atiende GET /v1/admin/users/{id}/dossier (HU #81).
func (h *DossierHandler) UserSummary(c *gin.Context) {
	summary, err := h.dossier.UserSummary(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, "el expediente del usuario", c.Param("id"), err)
		return
	}
	c.JSON(http.StatusOK, summary)
}

// UserSection atiende GET /v1/admin/users/{id}/dossier/{section}?limit=&offset=.
func (h *DossierHandler) UserSection(c *gin.Context) {
	limit, offset, ok := pageParams(c)
	if !ok {
		return
	}
	page, err := h.dossier.UserSection(c.Request.Context(), c.Param("id"), c.Param("section"), limit, offset)
	if err != nil {
		h.fail(c, "la sección del expediente del usuario", c.Param("id"), err)
		return
	}
	c.JSON(http.StatusOK, page)
}

// ServerSummary atiende GET /v1/admin/servers/{id}/dossier (HU #81).
func (h *DossierHandler) ServerSummary(c *gin.Context) {
	summary, err := h.dossier.ServerSummary(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, "el expediente del servidor", c.Param("id"), err)
		return
	}
	c.JSON(http.StatusOK, summary)
}

// ServerSection atiende GET /v1/admin/servers/{id}/dossier/{section}?limit=&offset=.
func (h *DossierHandler) ServerSection(c *gin.Context) {
	limit, offset, ok := pageParams(c)
	if !ok {
		return
	}
	page, err := h.dossier.ServerSection(c.Request.Context(), c.Param("id"), c.Param("section"), limit, offset)
	if err != nil {
		h.fail(c, "la sección del expediente del servidor", c.Param("id"), err)
		return
	}
	c.JSON(http.StatusOK, page)
}

// pageParams lee limit y offset con las mismas reglas que los listados. Si son
// inválidos responde 400 y devuelve ok=false.
func pageParams(c *gin.Context) (limit, offset int, ok bool) {
	limit, ok = apierr.ParseNonNegativeIntQuery(c, "limit", model.DefaultLimit)
	if !ok {
		return 0, 0, false
	}
	offset, ok = apierr.ParseNonNegativeIntQuery(c, "offset", 0)
	if !ok {
		return 0, 0, false
	}
	return limit, offset, true
}

func (h *DossierHandler) fail(c *gin.Context, what, id string, err error) {
	switch {
	case errors.Is(err, service.ErrUserNotFound):
		userNotFound(c)
	case errors.Is(err, service.ErrServerNotFound):
		serverNotFound(c)
	case errors.Is(err, service.ErrInvalidSection):
		apierr.Abort(c, http.StatusBadRequest, apierr.CodeInvalidSection, "La sección no existe para este expediente", map[string]any{"param": "section"})
	default:
		logging.Errorf("handler: no se pudo leer %s %q: %v", what, id, err)
		apierr.Internal(c)
	}
}
