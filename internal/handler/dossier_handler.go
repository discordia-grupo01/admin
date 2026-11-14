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

// sectionPage documenta la forma común de una página de sección: los ítems
// dependen de la sección (ver model.DossierPage). Solo se usa para el contrato.
type sectionPage struct {
	Items  []map[string]interface{} `json:"items"`
	Total  int                      `json:"total"`
	Limit  int                      `json:"limit"`
	Offset int                      `json:"offset"`
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
//
// @Summary      Expediente de investigación de un usuario (HU #81)
// @Description  Resumen para investigar a un usuario: su estado y fecha de registro, la fecha de su última actividad y, de cada sección (`servers`, `activity`, `reports`, `moderation`, `recovery_requests`), el total y los 5 ítems más recientes. Para ver más de una sección se pagina con `GET .../dossier/{section}`.
// @Description
// @Description  Las acciones de moderación mezclan las del staff de la plataforma (`scope: platform`) con las de los moderadores de cada servidor (`scope: server`). Los reportes incluyen los hechos contra el usuario y los de mensajes que escribió.
// @Tags         Dossier
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  string  true  "ID del usuario" format(uuid)
// @Success      200  {object}  model.UserDossierSummary  "Resumen del expediente"
// @Failure      401  {object}  apierr.ErrorResponse  "Falta el token, es inválido o venció (`SESSION_EXPIRED`)"
// @Failure      403  {object}  apierr.ErrorResponse  "El usuario no es staff de la plataforma (`FORBIDDEN`)"
// @Failure      404  {object}  apierr.ErrorResponse  "El usuario no existe, o todavía no llegó su registro (`USER_NOT_FOUND`)"
// @Router       /v1/admin/users/{id}/dossier [get]
func (h *DossierHandler) UserSummary(c *gin.Context) {
	summary, err := h.dossier.UserSummary(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, "el expediente del usuario", c.Param("id"), err)
		return
	}
	c.JSON(http.StatusOK, summary)
}

// UserSection atiende GET /v1/admin/users/{id}/dossier/{section}?limit=&offset=.
//
// @Summary      Una sección del expediente de un usuario, paginada (HU #81)
// @Description  Ordenada de la más nueva a la más vieja. Los `items` dependen de la sección: `servers` (membresías), `activity` (actividad), `reports` (reportes), `moderation` (acciones de moderación) o `recovery_requests` (solicitudes de recuperación de cuenta). El esquema de cada ítem es el de las listas `latest` del resumen.
// @Tags         Dossier
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  string  true  "ID del usuario" format(uuid)
// @Param        section  path  string  true  "Sección del expediente" Enums(servers, activity, reports, moderation, recovery_requests)
// @Param        limit   query  int  false  "Tamaño de página (máximo 100; un valor mayor se acota a 100)" minimum(0) maximum(100) default(20)
// @Param        offset  query  int  false  "Cantidad de ítems a saltear" minimum(0) default(0)
// @Success      200  {object}  sectionPage  "Una página de la sección (puede ser vacía)"
// @Failure      400  {object}  apierr.ErrorResponse  "La sección no existe (`INVALID_SECTION`) o `limit` / `offset` son inválidos (`INVALID_QUERY`)"
// @Failure      401  {object}  apierr.ErrorResponse  "Falta el token, es inválido o venció (`SESSION_EXPIRED`)"
// @Failure      403  {object}  apierr.ErrorResponse  "El usuario no es staff de la plataforma (`FORBIDDEN`)"
// @Failure      404  {object}  apierr.ErrorResponse  "El usuario no existe (`USER_NOT_FOUND`)"
// @Router       /v1/admin/users/{id}/dossier/{section} [get]
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
//
// @Summary      Expediente de investigación de un servidor (HU #81)
// @Description  Resumen para investigar un servidor: sus datos (fecha de creación, miembros, dueño, estado), los mensajes enviados en cada uno de los últimos 7 días y, de reportes y moderación, el total y los 5 ítems más recientes.
// @Description
// @Description  Los días de `messaging` son días **UTC**: un mensaje enviado a las 22 h en UTC-3 cuenta para el día siguiente. Siempre vienen los 7 días, del más viejo al más nuevo, con `messages: 0` donde no hubo actividad.
// @Description
// @Description  Un servidor eliminado responde `404`: servers lo borra de forma permanente y admin descarta sus reportes, su actividad y las acciones de sus moderadores. Se conservan las acciones del staff sobre el servidor en el expediente de los usuarios afectados.
// @Tags         Dossier
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  string  true  "ID del servidor" format(uuid)
// @Success      200  {object}  model.ServerDossierSummary  "Resumen del expediente"
// @Failure      401  {object}  apierr.ErrorResponse  "Falta el token, es inválido o venció (`SESSION_EXPIRED`)"
// @Failure      403  {object}  apierr.ErrorResponse  "El usuario no es staff de la plataforma (`FORBIDDEN`)"
// @Failure      404  {object}  apierr.ErrorResponse  "El servidor no existe, todavía no llegó su alta o fue eliminado (`SERVER_NOT_FOUND`)"
// @Router       /v1/admin/servers/{id}/dossier [get]
func (h *DossierHandler) ServerSummary(c *gin.Context) {
	summary, err := h.dossier.ServerSummary(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, "el expediente del servidor", c.Param("id"), err)
		return
	}
	c.JSON(http.StatusOK, summary)
}

// ServerSection atiende GET /v1/admin/servers/{id}/dossier/{section}?limit=&offset=.
//
// @Summary      Una sección del expediente de un servidor, paginada (HU #81)
// @Description  Ordenada de la más nueva a la más vieja. En `moderation` hay acciones del staff sobre el servidor (suspender, reactivar, pedir su eliminación) y las de sus moderadores sobre sus miembros. Los `items` son reportes o acciones de moderación, según la sección.
// @Tags         Dossier
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  string  true  "ID del servidor" format(uuid)
// @Param        section  path  string  true  "Sección del expediente" Enums(reports, moderation)
// @Param        limit   query  int  false  "Tamaño de página (máximo 100; un valor mayor se acota a 100)" minimum(0) maximum(100) default(20)
// @Param        offset  query  int  false  "Cantidad de ítems a saltear" minimum(0) default(0)
// @Success      200  {object}  sectionPage  "Una página de la sección (puede ser vacía)"
// @Failure      400  {object}  apierr.ErrorResponse  "La sección no existe (`INVALID_SECTION`) o `limit` / `offset` son inválidos (`INVALID_QUERY`)"
// @Failure      401  {object}  apierr.ErrorResponse  "Falta el token, es inválido o venció (`SESSION_EXPIRED`)"
// @Failure      403  {object}  apierr.ErrorResponse  "El usuario no es staff de la plataforma (`FORBIDDEN`)"
// @Failure      404  {object}  apierr.ErrorResponse  "El servidor no existe o fue eliminado (`SERVER_NOT_FOUND`)"
// @Router       /v1/admin/servers/{id}/dossier/{section} [get]
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
